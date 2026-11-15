// Package requestid lleva el identificador de petición que permite seguir los
// logs de un servicio a otro.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// Header es el encabezado HTTP que lleva el identificador de petición.
const Header = "X-Request-ID"

type contextKey struct{}

// WithContext guarda id en ctx.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext devuelve el identificador de petición guardado en ctx, o "".
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// New devuelve un identificador aleatorio de 128 bits en hexadecimal.
func New() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "unknown"
	}

	return hex.EncodeToString(buffer)
}
