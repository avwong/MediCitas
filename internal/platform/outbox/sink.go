package outbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// TokenSource entrega un token de cuenta de servicio.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// HTTPSink entrega los eventos al servicio central de Auditoría.
type HTTPSink struct {
	client *svcclient.Client
	tokens TokenSource
}

// NewHTTPSink devuelve un destino de auditoría por HTTP.
func NewHTTPSink(baseURL string, tokens TokenSource) *HTTPSink {
	return &HTTPSink{
		client: svcclient.New(baseURL),
		tokens: tokens,
	}
}

// Deliver envía un evento. Auditoría lo recibe de forma idempotente por
// event_id.
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
