// Package requestid carries the request identifier that correlates logs
// across services.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// Header is the HTTP header that carries the request identifier.
const Header = "X-Request-ID"

type contextKey struct{}

// WithContext stores id in ctx.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext returns the request identifier stored in ctx, or "".
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// New returns a random 128-bit identifier encoded as hex.
func New() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "unknown"
	}

	return hex.EncodeToString(buffer)
}
