package main

import (
	"log"
	"net/http"
	"os"

	"github.com/avwong/MediCitas/internal/auth"
	"github.com/gin-gonic/gin"
)

func main() {
	router := gin.New()

	router.Use(
		gin.Logger(),
		gin.Recovery(),
	)

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	authVerifier, err := auth.NewVerifier(
		os.Getenv("KEYCLOAK_ISSUER_URL"),
		os.Getenv("KEYCLOAK_JWKS_URL"),
		os.Getenv("KEYCLOAK_CLIENT_ID"),
	)
	if err != nil {
		log.Fatalf("configure OIDC verifier: %v", err)
	}

	protected := router.Group("/api/v1")
	protected.Use(authVerifier.Middleware())

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

		c.JSON(http.StatusOK, gin.H{
			"subject":  claims.Subject,
			"username": claims.PreferredUsername,
			"email":    claims.Email,
		})
	})

	protected.GET("/auth/clinic-admin-check", auth.RequirePermission(auth.PermissionClinicManage), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "clinic administration permission granted",
		})
	})

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	if err := router.Run(":" + port); err != nil {
		log.Fatalf("run HTTP server: %v", err)
	}
}
