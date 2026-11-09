package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/avwong/MediCitas/internal/auth"
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

	// Repository used to associate an authenticated Keycloak identity
	// with the corresponding MediCitas user and clinic.
	identityRepository := postgres.NewIdentityRepository(dbPool)

	// Tenant resolver derives UserID, ClinicID and Role from the
	// authenticated identity. The tenant is never trusted from request input.
	tenantResolver := tenancy.NewResolver(identityRepository)

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

	// Protected API.
	protected := router.Group("/api/v1")

	// First: authenticate the caller and validate the JWT.
	protected.Use(authVerifier.Middleware())

	// Second: resolve the authenticated identity to the internal
	// MediCitas user and tenant.
	protected.Use(tenantResolver.Middleware())

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

	protected.GET(
		"/auth/clinic-admin-check",
		auth.RequirePermission(auth.PermissionClinicManage),
		func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "clinic administration permission granted",
			})
		},
	)

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	if err := router.Run(":" + port); err != nil {
		log.Fatalf("run HTTP server: %v", err)
	}
}
