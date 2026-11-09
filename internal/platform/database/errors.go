package database

import (
	"errors"
	"fmt"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MapError translates PostgreSQL errors into domain errors. The original
// error stays wrapped for the logs; clients only see the domain kind.
func MapError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", apperr.ErrNotFound, err)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case "23505": // unique_violation
		return fmt.Errorf("%w: %w", apperr.ErrConflict, err)
	case "23P01": // exclusion_violation
		return fmt.Errorf("%w: %w", apperr.ErrConflict, err)
	case "23503": // foreign_key_violation
		// A reference to a row that does not exist in the caller's clinic.
		// It is reported as "not found" so the response does not reveal
		// whether the row exists in another clinic.
		return fmt.Errorf("%w: %w", apperr.ErrNotFound, err)
	case "23514", "23502", "22P02", "22001", "22007", "22008":
		// check, not null, invalid text, value too long, invalid datetime.
		return fmt.Errorf("%w: %w", apperr.ErrInvalidInput, err)
	default:
		return err
	}
}
