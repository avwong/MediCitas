package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/avwong/MediCitas/internal/platform/apperr"
)

func TestStatusFor(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{"invalid", apperr.ErrInvalidInput, http.StatusBadRequest, "invalid request"},
		{"invalid with message", apperr.Invalid("name is required"), http.StatusBadRequest, "name is required"},
		{"wrapped not found", fmt.Errorf("lookup: %w", apperr.ErrNotFound), http.StatusNotFound, "resource not found"},
		{"conflict", apperr.Conflict("slot taken"), http.StatusConflict, "slot taken"},
		{"forbidden", apperr.ErrForbidden, http.StatusForbidden, "forbidden"},
		{"unavailable", apperr.ErrUnavailable, http.StatusServiceUnavailable, "service temporarily unavailable"},
		{"unknown error hides details", errors.New("pq: connection refused at 10.0.0.3"), http.StatusInternalServerError, "internal server error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, message := StatusFor(tt.err)

			if status != tt.wantStatus || message != tt.wantMessage {
				t.Errorf("StatusFor = %d %q, want %d %q", status, message, tt.wantStatus, tt.wantMessage)
			}
		})
	}
}
