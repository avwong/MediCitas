// Package database abre los pools de conexiones a PostgreSQL, ejecuta
// transacciones con un nivel de aislamiento explícito y traduce los errores de
// PostgreSQL a errores de dominio.
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

// Config identifica la base de datos de un servicio y el rol de menor
// privilegio con el que se conecta.
type Config struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
}

// ConfigFromEnv lee DB_HOST, DB_PORT, DB_NAME, DB_USER y DB_PASSWORD.
func ConfigFromEnv(env *config.Loader) Config {
	return Config{
		Host:     env.Required("DB_HOST"),
		Port:     env.Optional("DB_PORT", "5432"),
		Database: env.Required("DB_NAME"),
		User:     env.Required("DB_USER"),
		Password: env.Required("DB_PASSWORD"),
	}
}

// DBTX lo cumplen *pgxpool.Pool, pgx.Tx y *Tx, así que los repositorios
// funcionan igual dentro y fuera de una transacción.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open se conecta a PostgreSQL y comprueba la conexión.
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
