package audit

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/avwong/MediCitas/internal/platform/outbox"
	"github.com/gin-gonic/gin"
)

// Handler exposes RF19 and the private producer intake.
type Handler struct {
	service   *Service
	logger    *slog.Logger
	producers map[string]string
}

// NewHandler returns the audit HTTP handler.
//
// producers maps Keycloak client id -> trusted source-service name.
func NewHandler(
	service *Service,
	logger *slog.Logger,
	producers map[string]string,
) *Handler {
	return &Handler{
		service:   service,
		logger:    logger,
		producers: producers,
	}
}

// RegisterRoutes adds the public RF19 route and private producer route.
func (h *Handler) RegisterRoutes(
	api *gin.RouterGroup,
	internal *gin.RouterGroup,
) {
	api.GET(
		"/audit",
		auth.RequirePermission(auth.PermAuditRead),
		h.List,
	)

	internal.POST("/audit/events", h.Ingest)
}

// Ingest handles POST /internal/audit/events.
func (h *Handler) Ingest(c *gin.Context) {
	actor, ok := auth.ActorFrom(c)
	if !ok {
		c.AbortWithStatusJSON(
			http.StatusUnauthorized,
			gin.H{"error": "unauthorized"},
		)
		return
	}

	source, ok := h.sourceFor(actor)
	if !ok {
		c.AbortWithStatusJSON(
			http.StatusForbidden,
			gin.H{"error": "forbidden"},
		)
		return
	}

	var event outbox.Event
	if !httpx.BindJSON(c, &event) {
		return
	}

	if err := h.service.Ingest(
		c.Request.Context(),
		event,
		source,
	); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	// Re-delivery of the same event is also successful.
	c.Status(http.StatusNoContent)
}

// List handles GET /api/v1/audit.
func (h *Handler) List(c *gin.Context) {
	actor, ok := auth.ActorFrom(c)
	if !ok {
		c.AbortWithStatusJSON(
			http.StatusUnauthorized,
			gin.H{"error": "unauthorized"},
		)
		return
	}

	filter, err := parseFilter(c)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	entries, err := h.service.List(
		c.Request.Context(),
		actor,
		filter,
	)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": entries})
}

func (h *Handler) sourceFor(actor auth.Actor) (string, bool) {
	if actor.Role != auth.RoleService {
		return "", false
	}

	source, ok := h.producers[actor.ClientID]
	return source, ok
}

func parseFilter(c *gin.Context) (Filter, error) {
	filter := Filter{
		User:      strings.TrimSpace(c.Query("user")),
		PatientID: strings.TrimSpace(c.Query("patient_id")),
	}

	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return Filter{}, apperr.Invalid(
				"from must be an RFC3339 timestamp",
			)
		}

		filter.From = &value
	}

	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return Filter{}, apperr.Invalid(
				"to must be an RFC3339 timestamp",
			)
		}

		filter.To = &value
	}

	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return Filter{}, apperr.Invalid(
				"limit must be an integer",
			)
		}

		filter.Limit = value
	}

	return filter, nil
}
