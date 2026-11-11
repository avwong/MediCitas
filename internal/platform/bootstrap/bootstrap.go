// Package bootstrap wires the pieces every service shares: logger, database
// pool, token verifier, router and health endpoints.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	// Embeds the IANA time zone database in the binary. The runtime image is
	// Alpine, which ships without it, and clinic time zones must resolve.
	_ "time/tzdata"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/config"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/avwong/MediCitas/internal/platform/logging"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// App holds a service's shared dependencies.
type App struct {
	Name     string
	Logger   *slog.Logger
	DB       *pgxpool.Pool
	Tx       *database.TxManager
	Verifier *auth.Verifier
	Router   *gin.Engine
	// API is the /api/v1 group. Every route registered on it requires a
	// valid access token.
	API *gin.RouterGroup

	addr string
}

// New reads the shared configuration from env, connects to the database and
// builds the router. Service-specific variables must be read from the same
// env before calling New, so every missing variable is reported together.
func New(ctx context.Context, name string, env *config.Loader) (*App, error) {
	dbConfig := database.ConfigFromEnv(env)

	verifierConfig := auth.VerifierConfig{
		IssuerURL: env.Required("OIDC_ISSUER_URL"),
		JWKSURL:   env.Required("OIDC_JWKS_URL"),
		Audience:  env.Optional("OIDC_AUDIENCE", "medicitas-api"),
	}

	addr := env.Optional("HTTP_ADDR", ":8080")

	if err := env.Err(); err != nil {
		return nil, err
	}

	logger := logging.New(name)

	pool, err := database.Open(ctx, dbConfig, name)
	if err != nil {
		return nil, err
	}

	verifier, err := auth.NewVerifier(ctx, verifierConfig)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("configure token verifier: %w", err)
	}

	router := httpx.NewRouter(logger)
	httpx.RegisterHealth(router, pool.Ping)

	return &App{
		Name:     name,
		Logger:   logger,
		DB:       pool,
		Tx:       database.NewTxManager(pool),
		Verifier: verifier,
		Router:   router,
		API:      router.Group("/api/v1", verifier.Authenticate()),
		addr:     addr,
	}, nil
}

// Run serves HTTP until ctx is cancelled and then releases the database pool.
func (a *App) Run(ctx context.Context) error {
	defer a.DB.Close()

	return httpx.Serve(ctx, a.addr, a.Router, a.Logger)
}
