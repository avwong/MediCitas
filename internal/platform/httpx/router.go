// Package httpx holds the HTTP plumbing shared by every service: router,
// middleware, error responses, health endpoints and the server lifecycle.
package httpx

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/avwong/MediCitas/internal/platform/requestid"
	"github.com/gin-gonic/gin"
)

// maxBodyBytes bounds every request body.
const maxBodyBytes = 1 << 20

// NewRouter returns a gin engine with request IDs, access logs, panic
// recovery and JSON responses for unknown routes.
func NewRouter(logger *slog.Logger) *gin.Engine {
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.HandleMethodNotAllowed = true

	router.Use(
		RequestID(),
		AccessLog(logger),
		Recovery(logger),
		limitBody(),
	)

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "route not found"})
	})

	router.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	})

	return router
}

// RequestID reuses the incoming X-Request-ID or generates one, stores it in
// the request context and echoes it in the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader(requestid.Header))
		if id == "" || len(id) > 128 {
			id = requestid.New()
		}

		ctx := requestid.WithContext(c.Request.Context(), id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(requestid.Header, id)

		c.Next()
	}
}

// AccessLog writes one structured record per request. It logs the route
// template, not the raw path, so identifiers do not end up in the logs.
func AccessLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		logger.InfoContext(
			c.Request.Context(),
			"http request",
			"request_id", requestid.FromContext(c.Request.Context()),
			"method", c.Request.Method,
			"route", route,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

// Recovery turns a panic into a logged 500 response.
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.ErrorContext(
			c.Request.Context(),
			"panic recovered",
			"request_id", requestid.FromContext(c.Request.Context()),
			"panic", recovered,
		)

		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
	})
}

func limitBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		}

		c.Next()
	}
}
