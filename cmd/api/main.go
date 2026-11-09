package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/avwong/MediCitas/internal/auth"
	"github.com/avwong/MediCitas/internal/platform"
	"github.com/avwong/MediCitas/internal/storage/postgres"
	"github.com/avwong/MediCitas/internal/tenancy"
	"github.com/gin-gonic/gin"
)

func main() {
	ctx := context.Background()

	// PostgreSQL configuration and connection pool.
	dbConfig, err := postgres.ConfigFromEnv()
	if err != nil {
		log.Fatalf("configure PostgreSQL: %v", err)
	}

	dbPool, err := postgres.Open(ctx, dbConfig)
	if err != nil {
		log.Fatalf("connect PostgreSQL: %v", err)
	}
	defer dbPool.Close()

	// Identity -> internal user -> tenant resolution.
	identityRepository := postgres.NewIdentityRepository(dbPool)
	tenantResolver := tenancy.NewResolver(identityRepository)

	// Platform application layers.
	platformRepository := postgres.NewPlatformRepository(dbPool)
	platformService := platform.NewService(platformRepository)
	platformHandler := platform.NewHandler(platformService)

	router := gin.New()

	router.Use(
		gin.Logger(),
		gin.Recovery(),
	)

	// Public liveness endpoint.
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// OIDC verifier validates access tokens issued by Keycloak.
	authVerifier, err := auth.NewVerifier(
		os.Getenv("KEYCLOAK_ISSUER_URL"),
		os.Getenv("KEYCLOAK_JWKS_URL"),
		os.Getenv("KEYCLOAK_CLIENT_ID"),
	)
	if err != nil {
		log.Fatalf("configure OIDC verifier: %v", err)
	}

	// All routes below require:
	// 1. a valid Keycloak token;
	// 2. a corresponding active MediCitas identity.
	protected := router.Group("/api/v1")
	protected.Use(authVerifier.Middleware())
	protected.Use(tenantResolver.Middleware())

	// Authentication/tenant inspection endpoint.
	protected.GET("/auth/me", func(c *gin.Context) {
		claimsValue, exists := c.Get(auth.ClaimsContextKey)
		if !exists {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "authentication context unavailable",
			})
			return
		}

		claims, ok := claimsValue.(*auth.Claims)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "invalid authentication context",
			})
			return
		}

		tenantContext, exists := tenancy.FromGinContext(c)
		if !exists {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "tenant context unavailable",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"subject":   claims.Subject,
			"username":  claims.PreferredUsername,
			"email":     claims.Email,
			"user_id":   tenantContext.UserID,
			"clinic_id": tenantContext.ClinicID,
			"role":      tenantContext.Role,
		})
	})

	// Diagnostic authorization endpoint.
	protected.GET(
		"/auth/clinic-admin-check",
		auth.RequirePermission(auth.PermissionClinicManage),
		func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "clinic administration permission granted",
			})
		},
	)

	// ---------------------------------------------------------------------
	// RF1 - Platform administration
	// ---------------------------------------------------------------------

	// Only a platform administrator may register a new clinic.
	protected.POST(
		"/platform/clinics",
		auth.RequirePermission(auth.PermissionPlatformManage),
		platformHandler.CreateClinic,
	)

	// ---------------------------------------------------------------------
	// RF2 - Clinic users
	// ---------------------------------------------------------------------

	protected.POST(
		"/users",
		auth.RequirePermission(auth.PermissionClinicManage),
		platformHandler.CreateUser,
	)

	protected.PATCH(
		"/users/:id/role",
		auth.RequirePermission(auth.PermissionClinicManage),
		platformHandler.UpdateUserRole,
	)

	protected.DELETE(
		"/users/:id",
		auth.RequirePermission(auth.PermissionClinicManage),
		platformHandler.DeactivateUser,
	)

	// ---------------------------------------------------------------------
	// RF3 - Clinic service catalog
	// ---------------------------------------------------------------------

	protected.POST(
		"/services",
		auth.RequirePermission(auth.PermissionClinicManage),
		platformHandler.CreateService,
	)

	// ---------------------------------------------------------------------
	// RF4 - Practitioners and supported services
	// ---------------------------------------------------------------------

	protected.POST(
		"/practitioners",
		auth.RequirePermission(auth.PermissionClinicManage),
		platformHandler.CreatePractitioner,
	)

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	if err := router.Run(":" + port); err != nil {
		log.Fatalf("run HTTP server: %v", err)
	}
}
