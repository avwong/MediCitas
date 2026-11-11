package outbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// TokenSource supplies a service-account token.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// HTTPSink delivers events to the central audit service.
type HTTPSink struct {
	client *svcclient.Client
	tokens TokenSource
}

// NewHTTPSink returns an HTTP audit sink.
func NewHTTPSink(baseURL string, tokens TokenSource) *HTTPSink {
	return &HTTPSink{
		client: svcclient.New(baseURL),
		tokens: tokens,
	}
}

// Deliver sends one event. Audit ingestion is idempotent by event_id.
func (s *HTTPSink) Deliver(ctx context.Context, event Event) error {
	token, err := s.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("obtain audit relay token: %w", err)
	}

	if err := s.client.Do(
		ctx,
		http.MethodPost,
		"/internal/audit/events",
		token,
		event,
		nil,
	); err != nil {
		return fmt.Errorf("deliver audit event %s: %w", event.EventID, err)
	}

	return nil
}
