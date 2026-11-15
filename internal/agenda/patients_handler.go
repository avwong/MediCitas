package agenda

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/gin-gonic/gin"
)

// PatientHandler expone los pacientes (RF14) por HTTP
// Vive aparte de Handler para no mezclar el catálogo con los pacientes
type PatientHandler struct {
	patients *Patients
	logger   *slog.Logger
}

// NewPatientHandler devuelve el handler HTTP de pacientes
func NewPatientHandler(patients *Patients, logger *slog.Logger) *PatientHandler {
	return &PatientHandler{patients: patients, logger: logger}
}

// RegisterRoutes agrega las rutas de pacientes a api
// api ya exige un token válido
// Los permisos siguen la matriz del enunciado para RF14
// Administración y recepción escriben, y el profesional solo lee
func (h *PatientHandler) RegisterRoutes(api *gin.RouterGroup) {
	write := auth.RequirePermission(auth.PermPatientsWrite)
	read := auth.RequirePermission(auth.PermPatientsRead)

	api.POST("/patients", write, h.CreatePatient)
	api.PATCH("/patients/:id", write, h.UpdatePatient)
	api.GET("/patients", read, h.ListPatients)
	api.GET("/patients/:id", read, h.GetPatient)
}

// createPatientRequest es el cuerpo de POST /api/v1/patients
// La clínica no viene en el cuerpo: se toma siempre del token firmado (I5)
type createPatientRequest struct {
	Identification string `json:"identification"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	BirthDate      string `json:"birth_date"`
	Phone          string `json:"phone"`
	Email          string `json:"email"`
	Address        string `json:"address"`
}

// updatePatientRequest es el cuerpo de PATCH /api/v1/patients/:id
// Los campos son punteros para distinguir "no enviado" de "enviado vacío"
// Un campo ausente no se modifica
// Un dato de contacto enviado vacío se borra
type updatePatientRequest struct {
	Identification *string `json:"identification"`
	FirstName      *string `json:"first_name"`
	LastName       *string `json:"last_name"`
	BirthDate      *string `json:"birth_date"`
	Phone          *string `json:"phone"`
	Email          *string `json:"email"`
	Address        *string `json:"address"`
}

// CreatePatient atiende POST /api/v1/patients
func (h *PatientHandler) CreatePatient(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request createPatientRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	patient, err := h.patients.Create(c.Request.Context(), actor, NewPatient{
		Identification: request.Identification,
		FirstName:      request.FirstName,
		LastName:       request.LastName,
		BirthDate:      request.BirthDate,
		Phone:          request.Phone,
		Email:          request.Email,
		Address:        request.Address,
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, patient)
}

// UpdatePatient atiende PATCH /api/v1/patients/:id
func (h *PatientHandler) UpdatePatient(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request updatePatientRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	patient, err := h.patients.Update(c.Request.Context(), actor, c.Param("id"), PatientUpdate{
		Identification: request.Identification,
		FirstName:      request.FirstName,
		LastName:       request.LastName,
		BirthDate:      request.BirthDate,
		Phone:          request.Phone,
		Email:          request.Email,
		Address:        request.Address,
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, patient)
}

// GetPatient atiende GET /api/v1/patients/:id
// Devuelve el paciente con sus datos de contacto
// Esta lectura queda registrada en la bitácora de auditoría
func (h *PatientHandler) GetPatient(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	patient, err := h.patients.Get(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, patient)
}

// ListPatients atiende GET /api/v1/patients
// Acepta los parámetros opcionales q (búsqueda por prefijo) y limit
// Devuelve solo datos básicos, sin contacto
func (h *PatientHandler) ListPatients(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	filter := PatientFilter{Query: c.Query("q")}

	// El límite es opcional
	// Si viene, debe ser un entero
	// El servicio valida el rango
	if rawLimit := c.Query("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			httpx.WriteError(c, h.logger, apperr.Invalid("limit must be an integer"))
			return
		}

		filter.Limit = limit
	}

	patients, err := h.patients.List(c.Request.Context(), actor, filter)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": patients})
}
