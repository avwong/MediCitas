// Package httpx reúne la infraestructura HTTP que comparten todos los
// servicios: router, middleware, respuestas de error, rutas de salud y ciclo
// de vida del servidor.
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

// maxBodyBytes limita el tamaño del cuerpo de cualquier petición.
const maxBodyBytes = 1 << 20

// NewRouter devuelve un motor de gin con ID de petición, log de accesos,
// recuperación de panics y respuestas JSON para las rutas desconocidas.
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

// RequestID reutiliza el X-Request-ID que llega o genera uno, lo guarda en el
// contexto de la petición y lo devuelve en la respuesta.
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

// AccessLog escribe un registro estructurado por petición. Registra la
// plantilla de la ruta y no la ruta real, para que los identificadores no
// terminen en los logs.
func AccessLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		route := c.FullPath()

		// Las sondas de salud llegan cada pocos segundos; registrarlas taparía
		// el tráfico real.
		if (route == "/health" || route == "/ready") && c.Writer.Status() < 400 {
			return
		}

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

// Recovery convierte un panic en una respuesta 500 que queda registrada.
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
