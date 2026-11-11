package outbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type fakeQueue struct {
	events []Event
	marked []string
}

func (f *fakeQueue) Pending(context.Context, int) ([]Event, error) {
	return f.events, nil
}

func (f *fakeQueue) MarkDelivered(_ context.Context, id string) error {
	f.marked = append(f.marked, id)
	return nil
}

type fakeSink struct {
	fail map[string]error
}

func (f *fakeSink) Deliver(_ context.Context, event Event) error {
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
