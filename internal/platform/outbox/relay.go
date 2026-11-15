package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// QueueStore es la persistencia que necesita Relay.
type QueueStore interface {
	Pending(ctx context.Context, limit int) ([]Event, error)
	MarkDelivered(ctx context.Context, eventID string) error
}

// Sink es el destino de los eventos de auditoría.
type Sink interface {
	Deliver(ctx context.Context, event Event) error
}

// Relay entrega cada cierto tiempo los eventos pendientes del outbox.
type Relay struct {
	store    QueueStore
	sink     Sink
	logger   *slog.Logger
	interval time.Duration
	batch    int
}

// NewRelay crea un relay de auditoría.
func NewRelay(
	store QueueStore,
	sink Sink,
	logger *slog.Logger,
	interval time.Duration,
	batch int,
) *Relay {
	if logger == nil {
		logger = slog.Default()
	}

	if interval <= 0 {
		interval = 2 * time.Second
	}

	if batch <= 0 {
		batch = 100
	}

	return &Relay{
		store:    store,
		sink:     sink,
		logger:   logger,
		interval: interval,
		batch:    batch,
	}
}

// RunOnce hace un ciclo de entrega.
//
// La entrega es "al menos una vez" a propósito. Si Auditoría aceptó un evento
// y este proceso se cae antes de MarkDelivered, el evento se envía de nuevo y
// Auditoría lo descarta como repetido por su event_id.
func (r *Relay) RunOnce(ctx context.Context) error {
	events, err := r.store.Pending(ctx, r.batch)
	if err != nil {
		return fmt.Errorf("read audit outbox: %w", err)
	}

	var result error

	for _, event := range events {
		if err := r.sink.Deliver(ctx, event); err != nil {
			result = errors.Join(
				result,
				fmt.Errorf("deliver %s: %w", event.EventID, err),
			)
			continue
		}

		if err := r.store.MarkDelivered(ctx, event.EventID); err != nil {
			result = errors.Join(
				result,
				fmt.Errorf("mark %s delivered: %w", event.EventID, err),
			)
		}
	}

	return result
}

// Run sigue entregando hasta que ctx se cancela.
func (r *Relay) Run(ctx context.Context) {
	run := func() {
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn(
				"audit outbox relay cycle failed",
				"error",
				err,
			)
		}
	}

	// El primer ciclo corre de inmediato, para no hacer esperar un intervalo a
	// los eventos que quedaron de una ejecución anterior.
	run()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
