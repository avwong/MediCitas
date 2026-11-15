package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/avwong/MediCitas/internal/platform/apperr"
)

// fakeQueue se comporta como el outbox real: Pending devuelve, hasta el
// límite, los eventos que todavía no están marcados como entregados.
type fakeQueue struct {
	events []Event
	marked []string
	limits []int
}

func (f *fakeQueue) Pending(_ context.Context, limit int) ([]Event, error) {
	f.limits = append(f.limits, limit)

	pending := make([]Event, 0, len(f.events))
	for _, event := range f.events {
		delivered := false
		for _, id := range f.marked {
			if id == event.EventID {
				delivered = true
			}
		}

		if !delivered && len(pending) < limit {
			pending = append(pending, event)
		}
	}

	return pending, nil
}

func (f *fakeQueue) MarkDelivered(_ context.Context, id string) error {
	f.marked = append(f.marked, id)
	return nil
}

type fakeSink struct {
	fail     map[string]error
	attempts map[string]int
}

func (f *fakeSink) Deliver(_ context.Context, event Event) error {
	if f.attempts == nil {
		f.attempts = make(map[string]int)
	}
	f.attempts[event.EventID]++

	return f.fail[event.EventID]
}

func TestRelayMarksOnlySuccessfulDeliveries(t *testing.T) {
	first := validEvent()

	second := validEvent()
	second.EventID = "44444444-4444-4444-8444-444444444444"

	store := &fakeQueue{
		events: []Event{first, second},
	}

	sink := &fakeSink{
		fail: map[string]error{
			first.EventID: errors.New("audit unavailable"),
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	relay := NewRelay(store, sink, logger, 0, 100)

	if err := relay.RunOnce(context.Background()); err == nil {
		t.Fatal("expected delivery failure")
	}

	if len(store.marked) != 1 {
		t.Fatalf("marked %d events, want 1", len(store.marked))
	}

	if store.marked[0] != second.EventID {
		t.Fatalf("marked %q, want %q", store.marked[0], second.EventID)
	}
}

// Un evento que Auditoría rechaza como inválido se intenta una sola vez y no
// impide entregar los demás, ni siquiera cuando el lote es de un solo evento.
func TestRelayStopsRetryingRejectedEvents(t *testing.T) {
	rejected := validEvent()

	accepted := validEvent()
	accepted.EventID = "44444444-4444-4444-8444-444444444444"

	store := &fakeQueue{
		events: []Event{rejected, accepted},
	}

	sink := &fakeSink{
		fail: map[string]error{
			rejected.EventID: fmt.Errorf("%w: audit returned 400", apperr.ErrInvalidInput),
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	relay := NewRelay(store, sink, logger, 0, 1)

	for cycle := 1; cycle <= 3; cycle++ {
		if err := relay.RunOnce(context.Background()); err != nil {
			t.Fatalf("cycle %d: unexpected error: %v", cycle, err)
		}
	}

	if got := sink.attempts[rejected.EventID]; got != 1 {
		t.Errorf("rejected event was sent %d times, want 1", got)
	}

	if len(store.marked) != 1 || store.marked[0] != accepted.EventID {
		t.Errorf("marked = %v, want only %q", store.marked, accepted.EventID)
	}

	if last := store.limits[len(store.limits)-1]; last != 2 {
		t.Errorf("last Pending limit = %d, want 2 (batch plus one rejected event)", last)
	}
}
