// Package bootstrap arma las piezas que comparten todos los servicios: logger,
// pool de base de datos, verificador de tokens, router y rutas de salud.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	// Incluye en el binario la base de zonas horarias de IANA. La imagen final
	// es Alpine, que no la trae, y las zonas de las clínicas deben resolverse.
	_ "time/tzdata"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/config"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/avwong/MediCitas/internal/platform/logging"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// App reúne las dependencias compartidas de un servicio.
type App struct {
	Name     string
	Logger   *slog.Logger
	DB       *pgxpool.Pool
	Tx       *database.TxManager
	Verifier *auth.Verifier
	Router   *gin.Engine
	// API es el grupo /api/v1. Toda ruta registrada en él exige un token de
	// acceso válido.
	API *gin.RouterGroup

	addr string
}

// New lee de env la configuración compartida, se conecta a la base de datos y
// construye el router. Las variables propias de cada servicio deben leerse del
// mismo env antes de llamar a New, para que todas las que falten se reporten
// juntas.
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

// Run atiende HTTP hasta que ctx se cancela y luego libera el pool de la base
// de datos.
func (a *App) Run(ctx context.Context) error {
	defer a.DB.Close()

	return httpx.Serve(ctx, a.addr, a.Router, a.Logger)
}
