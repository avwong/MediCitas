package database

import (
	"errors"
	"fmt"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MapError traduce los errores de PostgreSQL a errores de dominio. El error
// original queda envuelto para los logs; el cliente solo ve el tipo de
// dominio.
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
		// Una referencia a una fila que no existe en la clínica de quien
		// llama. Se reporta como "no encontrado" para no revelar si la fila
		// existe en otra clínica.
		return fmt.Errorf("%w: %w", apperr.ErrNotFound, err)
	case "23514", "23502", "22P02", "22001", "22007", "22008":
		// check, not null, texto inválido, valor demasiado largo y fecha
		// inválida.
		return fmt.Errorf("%w: %w", apperr.ErrInvalidInput, err)
	default:
		return err
	}
}
