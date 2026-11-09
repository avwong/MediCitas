package database

import (
	"errors"
	"fmt"
	"testing"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"no rows", pgx.ErrNoRows, apperr.ErrNotFound},
		{"unique violation", &pgconn.PgError{Code: "23505"}, apperr.ErrConflict},
		{"exclusion violation (overlapping appointment)", &pgconn.PgError{Code: "23P01"}, apperr.ErrConflict},
		{"foreign key violation (other clinic)", &pgconn.PgError{Code: "23503"}, apperr.ErrNotFound},
		{"check violation", &pgconn.PgError{Code: "23514"}, apperr.ErrInvalidInput},
		{"invalid uuid text", &pgconn.PgError{Code: "22P02"}, apperr.ErrInvalidInput},
		{"wrapped", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"}), apperr.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapError(tt.err); !errors.Is(got, tt.want) {
				t.Errorf("MapError = %v, want kind %v", got, tt.want)
			}
		})
	}
}

func TestMapErrorKeepsUnknownErrors(t *testing.T) {
	original := errors.New("connection reset")

	if got := MapError(original); got != original {
		t.Errorf("unknown errors must pass through unchanged, got %v", got)
	}

	if MapError(nil) != nil {
		t.Error("MapError(nil) must be nil")
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(&pgconn.PgError{Code: "40001"}) {
		t.Error("serialization failures must be retried")
	}

	if !IsRetryable(fmt.Errorf("commit: %w", &pgconn.PgError{Code: "40P01"})) {
		t.Error("deadlocks must be retried")
	}

	if IsRetryable(&pgconn.PgError{Code: "23505"}) {
		t.Error("unique violations must not be retried")
	}
}
