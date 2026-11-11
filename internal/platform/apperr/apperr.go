// Package apperr defines the error kinds shared by every service. Each kind
// maps to one HTTP status in package httpx.
package apperr

import "errors"

var (
	// ErrInvalidInput means the request is malformed or breaks a rule.
	ErrInvalidInput = errors.New("invalid input")
	// ErrNotFound means the resource does not exist for the caller's clinic.
	ErrNotFound = errors.New("resource not found")
	// ErrConflict means the request collides with the current state.
	ErrConflict = errors.New("resource conflict")
	// ErrForbidden means the caller may not perform the operation.
	ErrForbidden = errors.New("forbidden")
	// ErrUnavailable means a dependency (another service, Keycloak) failed.
	ErrUnavailable = errors.New("dependency unavailable")
)

// Error is an error of a known kind with a message that is safe to show to
// the client.
type Error struct {
	Kind    error
	Message string
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Kind }

// Invalid returns an ErrInvalidInput with a client-facing message.
func Invalid(message string) error {
	return &Error{Kind: ErrInvalidInput, Message: message}
}

// NotFound returns an ErrNotFound with a client-facing message.
func NotFound(message string) error {
	return &Error{Kind: ErrNotFound, Message: message}
}

// Conflict returns an ErrConflict with a client-facing message.
func Conflict(message string) error {
	return &Error{Kind: ErrConflict, Message: message}
}

// Forbidden returns an ErrForbidden with a client-facing message.
func Forbidden(message string) error {
	return &Error{Kind: ErrForbidden, Message: message}
}

// PublicMessage returns the client-facing message of err, if it has one.
func PublicMessage(err error) (string, bool) {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Message, true
	}

	return "", false
}
