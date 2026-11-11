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

// Tx is a transaction that can register work to run only after it commits,
// such as invalidating a cache entry.
type Tx struct {
	pgx.Tx
	afterCommit []func(context.Context)
}

// AfterCommit registers fn to run once the transaction has committed. It
// never runs if the transaction rolls back.
func (t *Tx) AfterCommit(fn func(context.Context)) {
	t.afterCommit = append(t.afterCommit, fn)
}

// TxOptions configures a transaction.
type TxOptions struct {
	// IsoLevel defaults to READ COMMITTED, PostgreSQL's default. It is set
	// explicitly so the level in use is always visible in the code.
	IsoLevel pgx.TxIsoLevel
	ReadOnly bool
	// ClinicID, when set, is exposed to the database as the app.clinic_id
	// setting for the duration of the transaction only (set_config with
	// is_local = true), so a pooled connection never carries a clinic into
	// another request. Row-level security policies can read it.
	ClinicID string
}

// TxManager runs transactions and retries the ones PostgreSQL aborts because
// of serialization failures or deadlocks.
type TxManager struct {
	pool        *pgxpool.Pool
	maxAttempts int
}

// NewTxManager returns a manager that makes up to three attempts.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool, maxAttempts: 3}
}

// Run executes fn inside a transaction. fn must be safe to run again: it is
// retried when PostgreSQL reports a serialization failure (40001) or a
// deadlock (40P01).
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

// IsRetryable reports whether err is a serialization failure or a deadlock.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}
