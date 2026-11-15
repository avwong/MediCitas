package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tx es una transacción que permite registrar trabajo que solo debe correr
// después del commit, como invalidar una entrada de caché.
type Tx struct {
	pgx.Tx
	afterCommit []func(context.Context)
}

// AfterCommit registra fn para que corra cuando la transacción haya hecho
// commit. Nunca corre si la transacción se revierte.
func (t *Tx) AfterCommit(fn func(context.Context)) {
	t.afterCommit = append(t.afterCommit, fn)
}

// TxOptions configura una transacción.
type TxOptions struct {
	// IsoLevel es READ COMMITTED si no se indica, igual que en PostgreSQL. Se
	// fija de forma explícita para que el nivel en uso siempre se vea en el
	// código.
	IsoLevel pgx.TxIsoLevel
	ReadOnly bool
	// ClinicID, si se indica, queda disponible en la base como el ajuste
	// app.clinic_id solo mientras dura la transacción (set_config con is_local
	// = true), de modo que una conexión del pool nunca arrastre la clínica a
	// otra petición. Las políticas de RLS pueden leerlo.
	ClinicID string
}

// TxManager ejecuta transacciones y reintenta las que PostgreSQL aborta por
// fallos de serialización o deadlocks.
type TxManager struct {
	pool        *pgxpool.Pool
	maxAttempts int
}

// NewTxManager devuelve un manager que hace hasta tres intentos.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool, maxAttempts: 3}
}

// Run ejecuta fn dentro de una transacción. fn debe poder ejecutarse de nuevo
// sin efectos indeseados: se reintenta cuando PostgreSQL reporta un fallo de
// serialización (40001) o un deadlock (40P01).
func (m *TxManager) Run(
	ctx context.Context,
	opts TxOptions,
	fn func(ctx context.Context, tx *Tx) error,
) error {
	var lastErr error

	for attempt := 1; attempt <= m.maxAttempts; attempt++ {
		err := m.runOnce(ctx, opts, fn)
		if err == nil {
			return nil
		}

		if !IsRetryable(err) {
			return err
		}

		lastErr = err

		backoff := time.Duration(attempt*attempt) * 10 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	return fmt.Errorf("transaction failed after %d attempts: %w", m.maxAttempts, lastErr)
}

func (m *TxManager) runOnce(
	ctx context.Context,
	opts TxOptions,
	fn func(ctx context.Context, tx *Tx) error,
) (err error) {
	isoLevel := opts.IsoLevel
	if isoLevel == "" {
		isoLevel = pgx.ReadCommitted
	}

	accessMode := pgx.ReadWrite
	if opts.ReadOnly {
		accessMode = pgx.ReadOnly
	}

	pgxTx, err := m.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   isoLevel,
		AccessMode: accessMode,
	})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	tx := &Tx{Tx: pgxTx}

	defer func() {
		if err != nil {
			_ = pgxTx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if opts.ClinicID != "" {
		_, err = tx.Exec(ctx, "SELECT set_config('app.clinic_id', $1, true)", opts.ClinicID)
		if err != nil {
			return fmt.Errorf("set clinic for transaction: %w", err)
		}
	}

	if err = fn(ctx, tx); err != nil {
		return err
	}

	if err = pgxTx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	for _, hook := range tx.afterCommit {
		hook(ctx)
	}

	return nil
}

// IsRetryable indica si err es un fallo de serialización o un deadlock.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}
