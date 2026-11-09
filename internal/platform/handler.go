package platform

import (
	"errors"
	"net/http"

	"github.com/avwong/MediCitas/internal/tenancy"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *ServiceLayer
}

func NewHandler(service *ServiceLayer) *Handler {
	return &Handler{
		service: service,
	}
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request",
		})

	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"error": "resource not found",
		})

	case errors.Is(err, ErrConflict):
		c.JSON(http.StatusConflict, gin.H{
			"error": "resource conflict",
		})

	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
	}
}

type createClinicRequest struct {
	Identifier   string `json:"identifier"`
	Name         string `json:"name"`
	Timezone     string `json:"timezone"`
	AdminSubject string `json:"admin_subject"`
}

type createUserRequest struct {
	Subject string `json:"keycloak_subject"`
	Role    string `json:"role"`
}

type updateRoleRequest struct {
	Role string `json:"role"`
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

// -----------------------------------------------------------------------------
// RF1 - Create clinic with first administrator
// -----------------------------------------------------------------------------

func (h *Handler) CreateClinic(c *gin.Context) {
	var request createClinicRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	clinic, admin, err := h.service.CreateClinic(
		c.Request.Context(),
		CreateClinicInput{
			Identifier:   request.Identifier,
			Name:         request.Name,
			Timezone:     request.Timezone,
			AdminSubject: request.AdminSubject,
		},
	)

	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"clinic": clinic,
		"admin":  admin,
	})
}

// -----------------------------------------------------------------------------
// RF2 - Clinic users
// -----------------------------------------------------------------------------

func (h *Handler) CreateUser(c *gin.Context) {
	tenantContext, ok := tenancy.FromGinContext(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "tenant context unavailable",
		})
		return
	}

	var request createUserRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	user, err := h.service.CreateUser(
		c.Request.Context(),
		tenantContext.ClinicID,
		CreateUserInput{
			Subject: request.Subject,
			Role:    request.Role,
		},
	)

	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusCreated, user)
}

func (h *Handler) UpdateUserRole(c *gin.Context) {
	tenantContext, ok := tenancy.FromGinContext(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "tenant context unavailable",
		})
		return
	}

	var request updateRoleRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	err := h.service.UpdateUserRole(
		c.Request.Context(),
		tenantContext.ClinicID,
		c.Param("id"),
		request.Role,
	)

	if err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) DeactivateUser(c *gin.Context) {
	tenantContext, ok := tenancy.FromGinContext(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "tenant context unavailable",
		})
		return
	}

	err := h.service.DeactivateUser(
		c.Request.Context(),
		tenantContext.ClinicID,
		c.Param("id"),
	)

	if err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// -----------------------------------------------------------------------------
// RF3 - Services
// -----------------------------------------------------------------------------

func (h *Handler) CreateService(c *gin.Context) {
	tenantContext, ok := tenancy.FromGinContext(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "tenant context unavailable",
		})
		return
	}

	var request createServiceRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	service, err := h.service.CreateService(
		c.Request.Context(),
		tenantContext.ClinicID,
		CreateServiceInput{
			Name:            request.Name,
			DurationMinutes: request.DurationMinutes,
			Specialty:       request.Specialty,
		},
	)

	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusCreated, service)
}

// -----------------------------------------------------------------------------
// RF4 - Practitioners
// -----------------------------------------------------------------------------

func (h *Handler) CreatePractitioner(c *gin.Context) {
	tenantContext, ok := tenancy.FromGinContext(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "tenant context unavailable",
		})
		return
	}

	var request createPractitionerRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}

	practitioner, err := h.service.CreatePractitioner(
		c.Request.Context(),
		tenantContext.ClinicID,
		CreatePractitionerInput{
			UserID:      request.UserID,
			DisplayName: request.DisplayName,
			Specialty:   request.Specialty,
			ServiceIDs:  request.ServiceIDs,
		},
	)

	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusCreated, practitioner)
}
