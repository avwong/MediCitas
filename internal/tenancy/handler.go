package tenancy

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/gin-gonic/gin"
)

// Handler exposes the tenancy service over HTTP.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler returns the tenancy HTTP handler.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// RegisterRoutes adds the routes to api, which already requires a valid
// token.
func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	api.GET("/me", h.Me)

	// RF1: platform administration.
	platform := api.Group("/platform", auth.RequirePermission(auth.PermClinicsManage))
	platform.POST("/clinics", h.CreateClinic)
	platform.GET("/clinics", h.ListClinics)

	// RF2: users of the caller's clinic.
	users := api.Group("/users", auth.RequirePermission(auth.PermUsersManage))
	users.POST("", h.CreateUser)
	users.GET("", h.ListUsers)
	users.GET("/:id", h.GetUser)
	users.PATCH("/:id/role", h.UpdateUserRole)
	users.DELETE("/:id", h.DeactivateUser)
}

type accountRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Password  string `json:"password"`
}

func (r accountRequest) account() Account {
	return Account{
		Username:  r.Username,
		Email:     r.Email,
		FirstName: r.FirstName,
		LastName:  r.LastName,
		Password:  r.Password,
	}
}

type createClinicRequest struct {
	Identifier string         `json:"identifier"`
	Name       string         `json:"name"`
	Timezone   string         `json:"timezone"`
	Admin      accountRequest `json:"admin"`
}

type createUserRequest struct {
	accountRequest
	Role string `json:"role"`
}

type updateRoleRequest struct {
	Role string `json:"role"`
}

// Me handles GET /api/v1/me: who the caller is according to the token, and
// their local record.
func (h *Handler) Me(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	response := gin.H{
		"subject":   actor.Subject,
		"username":  actor.Username,
		"role":      actor.Role,
		"clinic_id": actor.ClinicID,
	}

	user, err := h.service.Me(c.Request.Context(), actor)
	switch {
	case err == nil:
		response["user_id"] = user.ID
		response["active"] = user.Active
	case errors.Is(err, apperr.ErrNotFound):
		// Service accounts have no local record.
	default:
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// CreateClinic handles POST /api/v1/platform/clinics.
func (h *Handler) CreateClinic(c *gin.Context) {
	var request createClinicRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	clinic, admin, err := h.service.CreateClinic(c.Request.Context(), CreateClinicInput{
		Identifier: request.Identifier,
		Name:       request.Name,
		Timezone:   request.Timezone,
		Admin:      request.Admin.account(),
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"clinic": clinic, "admin": admin})
}

// ListClinics handles GET /api/v1/platform/clinics.
func (h *Handler) ListClinics(c *gin.Context) {
	clinics, err := h.service.ListClinics(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": clinics})
}

// CreateUser handles POST /api/v1/users.
func (h *Handler) CreateUser(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	var request createUserRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	user, err := h.service.CreateUser(c.Request.Context(), actor, CreateUserInput{
		Account: request.account(),
		Role:    request.Role,
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, user)
}

// ListUsers handles GET /api/v1/users.
func (h *Handler) ListUsers(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	users, err := h.service.ListUsers(c.Request.Context(), actor)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": users})
}

// GetUser handles GET /api/v1/users/:id.
func (h *Handler) GetUser(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	user, err := h.service.GetUser(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, user)
}

// UpdateUserRole handles PATCH /api/v1/users/:id/role.
func (h *Handler) UpdateUserRole(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	var request updateRoleRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	user, err := h.service.UpdateUserRole(c.Request.Context(), actor, c.Param("id"), request.Role)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, user)
}

// DeactivateUser handles DELETE /api/v1/users/:id (logical delete).
func (h *Handler) DeactivateUser(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}

	if err := h.service.DeactivateUser(c.Request.Context(), actor, c.Param("id")); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) actor(c *gin.Context) (auth.Actor, bool) {
	actor, ok := auth.ActorFrom(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return auth.Actor{}, false
	}

	return actor, true
}
