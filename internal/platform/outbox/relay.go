package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// QueueStore is the persistence boundary required by Relay.
type QueueStore interface {
	Pending(ctx context.Context, limit int) ([]Event, error)
	MarkDelivered(ctx context.Context, eventID string) error
}

// Sink receives audit events.
type Sink interface {
	Deliver(ctx context.Context, event Event) error
}

// Relay periodically delivers pending outbox events.
type Relay struct {
	store    QueueStore
	sink     Sink
	logger   *slog.Logger
	interval time.Duration
	batch    int
}

// NewRelay creates an audit relay.
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

// RunOnce performs one delivery cycle.
//
// Delivery is intentionally at-least-once. If the audit service accepted an
// event and this process crashes before MarkDelivered, the event is sent
// again. The audit service deduplicates it by event_id.
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

// Run keeps retrying until ctx is cancelled.
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

	// Do not wait one interval before delivering events left from a previous run.
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
