// Package database opens PostgreSQL connection pools, runs transactions with
// an explicit isolation level and maps PostgreSQL errors to domain errors.
package database

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/avwong/MediCitas/internal/platform/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const connectTimeout = 5 * time.Second

// Config identifies one service database and the least-privilege role the
// service connects with.
type Config struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
}

// ConfigFromEnv reads DB_HOST, DB_PORT, DB_NAME, DB_USER and DB_PASSWORD.
func ConfigFromEnv(env *config.Loader) Config {
	return Config{
		Host:     env.Required("DB_HOST"),
		Port:     env.Optional("DB_PORT", "5432"),
		Database: env.Required("DB_NAME"),
		User:     env.Required("DB_USER"),
		Password: env.Required("DB_PASSWORD"),
	}
}

// DBTX is satisfied by *pgxpool.Pool, pgx.Tx and *Tx, so repositories work
// the same inside and outside a transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open connects to PostgreSQL and verifies the connection.
func Open(ctx context.Context, cfg Config, applicationName string) (*pgxpool.Pool, error) {
	dsn := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Path:     cfg.Database,
		RawQuery: url.Values{"sslmode": []string{"disable"}}.Encode(),
	}).String()

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}

	poolConfig.ConnConfig.RuntimeParams["application_name"] = applicationName

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}

	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}

	return pool, nil
}
