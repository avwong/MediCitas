package agenda

import (
	"log/slog"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/gin-gonic/gin"
)

// Handler exposes the agenda over HTTP.
type Handler struct {
	catalog *Catalog
	logger  *slog.Logger
}

// NewHandler returns the agenda HTTP handler.
func NewHandler(catalog *Catalog, logger *slog.Logger) *Handler {
	return &Handler{catalog: catalog, logger: logger}
}

// RegisterRoutes adds the public routes to api, which already requires a
// valid token.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	manage := auth.RequirePermission(auth.PermCatalogManage)
	read := auth.RequirePermission(auth.PermCatalogRead)

	// RF3: service catalog.
	api.POST("/services", manage, h.CreateService)
	api.GET("/services", read, h.ListServices)
	api.GET("/services/:id", read, h.GetService)
	api.DELETE("/services/:id", manage, h.DeactivateService)

	// RF4: practitioners and the services they attend.
	api.POST("/practitioners", manage, h.CreatePractitioner)
	api.GET("/practitioners", read, h.ListPractitioners)
	api.GET("/practitioners/:id", read, h.GetPractitioner)
	api.PUT("/practitioners/:id/services", manage, h.ReplacePractitionerServices)
}

// RegisterInternalRoutes adds the routes other services call. internal must
// already require a valid token; only tenancyClientID's service account may
// call them. The proxy does not expose /internal.
func (h *Handler) RegisterInternalRoutes(internal *gin.RouterGroup, tenancyClientID string) {
	onlyTenancy := auth.RequireService(tenancyClientID)

	internal.PUT("/clinics/:id", onlyTenancy, h.ProvisionClinic)
	internal.DELETE("/clinics/:id", onlyTenancy, h.DeprovisionClinic)
}

type provisionClinicRequest struct {
	Timezone string `json:"timezone"`
}

type createServiceRequest struct {
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
	Specialty       string `json:"specialty"`
}

type createPractitionerRequest struct {
	UserID      string   `json:"user_id"`
	DisplayName string   `json:"display_name"`
	Specialty   string   `json:"specialty"`
	ServiceIDs  []string `json:"service_ids"`
}

type replaceServicesRequest struct {
	ServiceIDs []string `json:"service_ids"`
}

// ProvisionClinic handles PUT /internal/clinics/:id.
func (h *Handler) ProvisionClinic(c *gin.Context) {
	var request provisionClinicRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	if err := h.catalog.ProvisionClinic(c.Request.Context(), c.Param("id"), request.Timezone); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// DeprovisionClinic handles DELETE /internal/clinics/:id.
func (h *Handler) DeprovisionClinic(c *gin.Context) {
	if err := h.catalog.DeprovisionClinic(c.Request.Context(), c.Param("id")); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// CreateService handles POST /api/v1/services.
func (h *Handler) CreateService(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	var request createServiceRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	service, err := h.catalog.CreateService(c.Request.Context(), actor, NewMedicalService{
		Name:            request.Name,
		DurationMinutes: request.DurationMinutes,
		Specialty:       request.Specialty,
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, service)
}

// ListServices handles GET /api/v1/services.
func (h *Handler) ListServices(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	services, err := h.catalog.ListServices(c.Request.Context(), actor)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": services})
}

// GetService handles GET /api/v1/services/:id.
func (h *Handler) GetService(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	service, err := h.catalog.GetService(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, service)
}

// DeactivateService handles DELETE /api/v1/services/:id.
func (h *Handler) DeactivateService(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	if err := h.catalog.DeactivateService(c.Request.Context(), actor, c.Param("id")); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// CreatePractitioner handles POST /api/v1/practitioners.
func (h *Handler) CreatePractitioner(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	var request createPractitionerRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	practitioner, err := h.catalog.CreatePractitioner(
		c.Request.Context(),
		actor,
		NewPractitioner{
			UserID:      request.UserID,
			DisplayName: request.DisplayName,
			Specialty:   request.Specialty,
		},
		request.ServiceIDs,
	)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, practitioner)
}

// ListPractitioners handles GET /api/v1/practitioners.
func (h *Handler) ListPractitioners(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	practitioners, err := h.catalog.ListPractitioners(c.Request.Context(), actor)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": practitioners})
}

// GetPractitioner handles GET /api/v1/practitioners/:id.
func (h *Handler) GetPractitioner(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	practitioner, err := h.catalog.GetPractitioner(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, practitioner)
}

// ReplacePractitionerServices handles PUT /api/v1/practitioners/:id/services.
func (h *Handler) ReplacePractitionerServices(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	var request replaceServicesRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	practitioner, err := h.catalog.ReplacePractitionerServices(
		c.Request.Context(),
		actor,
		c.Param("id"),
		request.ServiceIDs,
	)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, practitioner)
}

func (h *Handler) actor(c *gin.Context) (auth.Actor, bool) {
	actor, ok := auth.ActorFrom(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return auth.Actor{}, false
	}

	return actor, true
}
