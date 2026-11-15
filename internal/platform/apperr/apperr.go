// Package apperr define los tipos de error que comparten todos los servicios.
// El paquete httpx traduce cada tipo a un código HTTP.
package apperr

import "errors"

var (
	// ErrInvalidInput indica que la petición está mal formada o incumple una
	// regla.
	ErrInvalidInput = errors.New("invalid input")
	// ErrNotFound indica que el recurso no existe en la clínica de quien
	// llama.
	ErrNotFound = errors.New("resource not found")
	// ErrConflict indica que la petición choca con el estado actual.
	ErrConflict = errors.New("resource conflict")
	// ErrForbidden indica que quien llama no puede realizar la operación.
	ErrForbidden = errors.New("forbidden")
	// ErrUnavailable indica que falló una dependencia (otro servicio o
	// Keycloak).
	ErrUnavailable = errors.New("dependency unavailable")
)

// Error es un error de un tipo conocido con un mensaje que se puede mostrar al
// cliente sin riesgo.
type Error struct {
	Kind    error
	Message string
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Kind }

// Invalid devuelve un ErrInvalidInput con un mensaje para el cliente.
func Invalid(message string) error {
	return &Error{Kind: ErrInvalidInput, Message: message}
}

// NotFound devuelve un ErrNotFound con un mensaje para el cliente.
func NotFound(message string) error {
	return &Error{Kind: ErrNotFound, Message: message}
}

// Conflict devuelve un ErrConflict con un mensaje para el cliente.
func Conflict(message string) error {
	return &Error{Kind: ErrConflict, Message: message}
}

// Forbidden devuelve un ErrForbidden con un mensaje para el cliente.
func Forbidden(message string) error {
	return &Error{Kind: ErrForbidden, Message: message}
}

// PublicMessage devuelve el mensaje para el cliente de err, si lo tiene.
func PublicMessage(err error) (string, bool) {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Message, true
	}

	return "", false
}
