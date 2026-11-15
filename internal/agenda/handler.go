package agenda

import (
	"log/slog"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/gin-gonic/gin"
)

// Handler expone el catálogo de Agenda por HTTP.
type Handler struct {
	catalog *Catalog
	logger  *slog.Logger
}

// NewHandler devuelve el handler HTTP del catálogo.
func NewHandler(catalog *Catalog, logger *slog.Logger) *Handler {
	return &Handler{catalog: catalog, logger: logger}
}

// RegisterRoutes agrega las rutas públicas a api, que ya exige un token
// válido.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	manage := auth.RequirePermission(auth.PermCatalogManage)
	read := auth.RequirePermission(auth.PermCatalogRead)

	// RF3: catálogo de servicios.
	api.POST("/services", manage, h.CreateService)
	api.GET("/services", read, h.ListServices)
	api.GET("/services/:id", read, h.GetService)
	api.DELETE("/services/:id", manage, h.DeactivateService)

	// RF4: profesionales y los servicios que atienden.
	api.POST("/practitioners", manage, h.CreatePractitioner)
	api.GET("/practitioners", read, h.ListPractitioners)
	api.GET("/practitioners/:id", read, h.GetPractitioner)
	api.PUT("/practitioners/:id/services", manage, h.ReplacePractitionerServices)
}

// RegisterInternalRoutes agrega las rutas que usan otros servicios. internal
// ya debe exigir un token válido; solo la cuenta de servicio de
// tenancyClientID puede llamarlas. El proxy no expone /internal.
func (h *Handler) RegisterInternalRoutes(internal *gin.RouterGroup, tenancyClientID string) {
	onlyTenancy := auth.RequireService(tenancyClientID)

	internal.PUT("/clinics/:id", onlyTenancy, h.ProvisionClinic)
	internal.DELETE("/clinics/:id", onlyTenancy, h.DeprovisionClinic)
	internal.PUT("/practitioners/by-user/:user_id/active", onlyTenancy, h.SetPractitionerActive)
}

type provisionClinicRequest struct {
	Timezone string `json:"timezone"`
}

// setPractitionerActiveRequest usa un puntero para exigir el campo: sin él, un
// cuerpo vacío se leería como "desactivar".
type setPractitionerActiveRequest struct {
	Active *bool `json:"active"`
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

// ProvisionClinic atiende PUT /internal/clinics/:id.
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

// DeprovisionClinic atiende DELETE /internal/clinics/:id.
func (h *Handler) DeprovisionClinic(c *gin.Context) {
	if err := h.catalog.DeprovisionClinic(c.Request.Context(), c.Param("id")); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// SetPractitionerActive atiende PUT
// /internal/practitioners/by-user/:user_id/active.
func (h *Handler) SetPractitionerActive(c *gin.Context) {
	var request setPractitionerActiveRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	if request.Active == nil {
		httpx.WriteError(c, h.logger, apperr.Invalid("active is required"))
		return
	}

	if err := h.catalog.SetPractitionerActive(c.Request.Context(), c.Param("user_id"), *request.Active); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// CreateService atiende POST /api/v1/services.
func (h *Handler) CreateService(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
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

// ListServices atiende GET /api/v1/services.
func (h *Handler) ListServices(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
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

// GetService atiende GET /api/v1/services/:id.
func (h *Handler) GetService(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
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

// DeactivateService atiende DELETE /api/v1/services/:id.
func (h *Handler) DeactivateService(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	if err := h.catalog.DeactivateService(c.Request.Context(), actor, c.Param("id")); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// CreatePractitioner atiende POST /api/v1/practitioners.
func (h *Handler) CreatePractitioner(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
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

// ListPractitioners atiende GET /api/v1/practitioners.
func (h *Handler) ListPractitioners(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
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

// GetPractitioner atiende GET /api/v1/practitioners/:id.
func (h *Handler) GetPractitioner(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
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

// ReplacePractitionerServices atiende PUT /api/v1/practitioners/:id/services.
func (h *Handler) ReplacePractitionerServices(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
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
