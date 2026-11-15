package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/requestid"
	"github.com/gin-gonic/gin"
)

// StatusFor traduce un error a su código HTTP y a un mensaje que se puede
// mostrar al cliente sin riesgo.
func StatusFor(err error) (int, string) {
	status, fallback := statusAndDefault(err)

	if message, ok := apperr.PublicMessage(err); ok && status != http.StatusInternalServerError {
		return status, message
	}

	return status, fallback
}

func statusAndDefault(err error) (int, string) {
	switch {
	case errors.Is(err, apperr.ErrInvalidInput):
		return http.StatusBadRequest, "invalid request"
	case errors.Is(err, apperr.ErrNotFound):
		return http.StatusNotFound, "resource not found"
	case errors.Is(err, apperr.ErrConflict):
		return http.StatusConflict, "resource conflict"
	case errors.Is(err, apperr.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, apperr.ErrUnavailable):
		return http.StatusServiceUnavailable, "service temporarily unavailable"
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}

// WriteError responde la petición con el código que corresponde a err. Los
// errores internos y de dependencias se registran con el ID de la petición; el
// cliente solo recibe un mensaje genérico.
func WriteError(c *gin.Context, logger *slog.Logger, err error) {
	status, message := StatusFor(err)

	switch {
	case status >= http.StatusInternalServerError && status != http.StatusServiceUnavailable:
		logger.ErrorContext(
			c.Request.Context(),
			"request failed",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", err.Error(),
		)
	case status == http.StatusServiceUnavailable:
		logger.WarnContext(
			c.Request.Context(),
			"dependency unavailable",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", err.Error(),
		)
	}

	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// BindJSON decodifica el cuerpo de la petición en dst y responde 400 si está
// mal formado. Indica si el handler puede continuar.
func BindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return false
	}

	return true
}
