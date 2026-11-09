package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultConnectTimeout = 5 * time.Second

type Config struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
}

func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Host:     os.Getenv("DB_HOST"),
		Port:     os.Getenv("DB_PORT"),
		Database: os.Getenv("DB_NAME"),
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
	}

	if cfg.Host == "" {
		return Config{}, errors.New("DB_HOST is required")
	}
	if cfg.Port == "" {
		return Config{}, errors.New("DB_PORT is required")
	}
	if cfg.Database == "" {
		return Config{}, errors.New("DB_NAME is required")
	}
	if cfg.User == "" {
		return Config{}, errors.New("DB_USER is required")
	}
	if cfg.Password == "" {
		return Config{}, errors.New("DB_PASSWORD is required")
	}

	return cfg, nil
}

func Open(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	dsn := (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Path:   cfg.Database,
		RawQuery: url.Values{
			"sslmode": []string{"disable"},
		}.Encode(),
	}).String()

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, defaultConnectTimeout)
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
