package expediente

import (
	"log/slog"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/gin-gonic/gin"
)

// Handler exposes RF15 over HTTP.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler returns the clinical-note HTTP handler.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterRoutes adds the RF15 routes to an already-authenticated API group.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	ownNotes := auth.RequirePermission(auth.PermNotesOwn)

	notes := api.Group(
		"/clinical-notes",
		ownNotes,
	)

	notes.POST("", h.Create)
	notes.GET("/:id", h.Get)
}

type createClinicalNoteRequest struct {
	AppointmentID string `json:"appointment_id"`
	Content       string `json:"content"`
}

// Create handles POST /api/v1/clinical-notes.
func (h *Handler) Create(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	var request createClinicalNoteRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	note, err := h.service.Create(
		c.Request.Context(),
		actor,
		CreateNoteInput{
			AppointmentID: request.AppointmentID,
			Content:       request.Content,
		},
	)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, note)
}

// Get handles GET /api/v1/clinical-notes/:id.
func (h *Handler) Get(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	note, err := h.service.Get(
		c.Request.Context(),
		actor,
		c.Param("id"),
	)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, note)
}

func (h *Handler) actor(c *gin.Context) (auth.Actor, bool) {
	actor, ok := auth.ActorFrom(c)
	if !ok {
		c.AbortWithStatusJSON(
			http.StatusUnauthorized,
			gin.H{"error": "unauthorized"},
		)
		return auth.Actor{}, false
	}

	return actor, true
}
