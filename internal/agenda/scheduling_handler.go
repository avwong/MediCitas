package agenda

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
)

// SchedulingHandler expone por HTTP los horarios, los bloqueos, la
// disponibilidad y las citas.
type SchedulingHandler struct {
	scheduling *Scheduling
	logger     *slog.Logger
}

// NewSchedulingHandler devuelve el handler HTTP de la agenda.
func NewSchedulingHandler(scheduling *Scheduling, logger *slog.Logger) *SchedulingHandler {
	return &SchedulingHandler{scheduling: scheduling, logger: logger}
}

// RegisterRoutes agrega las rutas a api, que ya exige un token válido. Los
// permisos siguen la matriz del enunciado; la regla "solo los propios" de los
// profesionales para RF5 y RF6 se aplica dentro del servicio.
func (h *SchedulingHandler) RegisterRoutes(api *gin.RouterGroup) {
	manageSchedule := auth.RequirePermission(auth.PermScheduleManage)
	readAvailability := auth.RequirePermission(auth.PermAvailabilityRead)
	book := auth.RequirePermission(auth.PermAppointmentsBook)
	outcome := auth.RequirePermission(auth.PermAppointmentsOutcome)

	// RF5: horario semanal.
	api.GET("/practitioners/:id/schedule", readAvailability, h.GetSchedule)
	api.PUT("/practitioners/:id/schedule", manageSchedule, h.ReplaceSchedule)

	// RF6: bloqueos y sus citas en conflicto.
	api.POST("/practitioners/:id/blocks", manageSchedule, h.CreateBlock)
	api.GET("/practitioners/:id/blocks", readAvailability, h.ListBlocks)
	api.GET("/practitioners/:id/blocks/:block_id/conflicts", readAvailability, h.BlockConflicts)
	api.DELETE("/practitioners/:id/blocks/:block_id", manageSchedule, h.DeleteBlock)

	// RF7 y RF8: disponibilidad.
	api.GET("/practitioners/:id/availability", readAvailability, h.Availability)
	api.GET("/availability", readAvailability, h.AvailabilityByService)

	// RF9 a RF13: ciclo de vida de la cita. Leer una cita no está en la
	// matriz; se concede a quienes pueden registrar su desenlace, que son
	// los tres roles de la clínica.
	api.POST("/appointments", book, h.Book)
	api.GET("/appointments/:id", outcome, h.GetAppointment)
	api.POST("/appointments/:id/reschedule", book, h.Reschedule)
	api.POST("/appointments/:id/cancel", book, h.Cancel)
	api.POST("/appointments/:id/outcome", outcome, h.RecordOutcome)
}

// RegisterInternalRoutes agrega la ruta que usa Expediente. internal ya debe
// exigir un token válido. Expediente reenvía el token del profesional que
// escribe la nota, así que se pide el mismo permiso que para escribirla.
func (h *SchedulingHandler) RegisterInternalRoutes(internal *gin.RouterGroup) {
	internal.GET(
		"/appointments/:id/clinical-note-context",
		auth.RequirePermission(auth.PermNotesOwn),
		h.ClinicalNoteContext,
	)
}

type replaceScheduleRequest struct {
	Blocks []ScheduleBlock `json:"blocks"`
}

type createBlockRequest struct {
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
	Reason   string `json:"reason"`
}

// bookRequest es el cuerpo de POST /api/v1/appointments. La clínica no viene
// en el cuerpo: se toma siempre del token firmado (I5).
type bookRequest struct {
	PatientID      string `json:"patient_id"`
	PractitionerID string `json:"practitioner_id"`
	ServiceID      string `json:"service_id"`
	StartsAt       string `json:"starts_at"`
}

type rescheduleRequest struct {
	StartsAt       string `json:"starts_at"`
	PractitionerID string `json:"practitioner_id"`
}

type cancelRequest struct {
	Reason string `json:"reason"`
}

type outcomeRequest struct {
	Outcome string `json:"outcome"`
}

// GetSchedule atiende GET /api/v1/practitioners/:id/schedule.
func (h *SchedulingHandler) GetSchedule(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	blocks, err := h.scheduling.Schedule(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"blocks": blocks})
}

// ReplaceSchedule atiende PUT /api/v1/practitioners/:id/schedule. Responde el
// horario nuevo y las citas vigentes que quedaron fuera de él.
func (h *SchedulingHandler) ReplaceSchedule(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request replaceScheduleRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	blocks, conflicts, err := h.scheduling.ReplaceSchedule(c.Request.Context(), actor, c.Param("id"), request.Blocks)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"blocks": blocks, "conflicts": conflicts})
}

// CreateBlock atiende POST /api/v1/practitioners/:id/blocks. Responde el
// bloqueo y las citas vigentes que caen dentro de él.
func (h *SchedulingHandler) CreateBlock(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request createBlockRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	block, conflicts, err := h.scheduling.CreateBlock(c.Request.Context(), actor, c.Param("id"), NewAgendaBlock{
		StartsAt: request.StartsAt,
		EndsAt:   request.EndsAt,
		Reason:   request.Reason,
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"block": block, "conflicts": conflicts})
}

// ListBlocks atiende GET /api/v1/practitioners/:id/blocks, con los parámetros
// opcionales from y to (AAAA-MM-DD).
func (h *SchedulingHandler) ListBlocks(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	blocks, err := h.scheduling.Blocks(c.Request.Context(), actor, c.Param("id"), c.Query("from"), c.Query("to"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": blocks})
}

// BlockConflicts atiende GET
// /api/v1/practitioners/:id/blocks/:block_id/conflicts.
func (h *SchedulingHandler) BlockConflicts(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	conflicts, err := h.scheduling.BlockConflicts(c.Request.Context(), actor, c.Param("id"), c.Param("block_id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": conflicts})
}

// DeleteBlock atiende DELETE /api/v1/practitioners/:id/blocks/:block_id.
func (h *SchedulingHandler) DeleteBlock(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	if err := h.scheduling.DeleteBlock(c.Request.Context(), actor, c.Param("id"), c.Param("block_id")); err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// Availability atiende GET /api/v1/practitioners/:id/availability, con los
// parámetros date (AAAA-MM-DD) y service_id. El encabezado X-Cache indica si
// la respuesta salió de la caché (hit) o se calculó contra la base (miss).
func (h *SchedulingHandler) Availability(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	slots, cached, err := h.scheduling.Availability(
		c.Request.Context(),
		actor,
		c.Param("id"),
		c.Query("service_id"),
		c.Query("date"),
	)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	if cached {
		c.Header("X-Cache", "hit")
	} else {
		c.Header("X-Cache", "miss")
	}

	c.JSON(http.StatusOK, gin.H{
		"practitioner_id": c.Param("id"),
		"service_id":      c.Query("service_id"),
		"date":            c.Query("date"),
		"slots":           slots,
	})
}

// AvailabilityByService atiende GET /api/v1/availability, con los parámetros
// service_id, from y to (AAAA-MM-DD).
func (h *SchedulingHandler) AvailabilityByService(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	items, err := h.scheduling.AvailabilityByService(
		c.Request.Context(),
		actor,
		c.Query("service_id"),
		c.Query("from"),
		c.Query("to"),
	)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": items})
}

// Book atiende POST /api/v1/appointments. Responde 201 si la cita se
// concedió, 409 si el espacio no está disponible y 400 si la solicitud es
// inválida.
func (h *SchedulingHandler) Book(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request bookRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	appointment, err := h.scheduling.Book(c.Request.Context(), actor, NewBooking{
		PatientID:      request.PatientID,
		PractitionerID: request.PractitionerID,
		ServiceID:      request.ServiceID,
		StartsAt:       request.StartsAt,
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, appointment)
}

// GetAppointment atiende GET /api/v1/appointments/:id.
func (h *SchedulingHandler) GetAppointment(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	appointment, err := h.scheduling.GetAppointment(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, appointment)
}

// Reschedule atiende POST /api/v1/appointments/:id/reschedule. Responde 201
// con la cita nueva, que apunta a la anterior en rescheduled_from_id.
func (h *SchedulingHandler) Reschedule(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request rescheduleRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	appointment, err := h.scheduling.Reschedule(c.Request.Context(), actor, c.Param("id"), Rescheduling{
		StartsAt:       request.StartsAt,
		PractitionerID: request.PractitionerID,
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusCreated, appointment)
}

// Cancel atiende POST /api/v1/appointments/:id/cancel.
func (h *SchedulingHandler) Cancel(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request cancelRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	appointment, err := h.scheduling.Cancel(c.Request.Context(), actor, c.Param("id"), request.Reason)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, appointment)
}

// RecordOutcome atiende POST /api/v1/appointments/:id/outcome.
func (h *SchedulingHandler) RecordOutcome(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	var request outcomeRequest
	if !httpx.BindJSON(c, &request) {
		return
	}

	appointment, err := h.scheduling.RecordOutcome(c.Request.Context(), actor, c.Param("id"), request.Outcome)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, appointment)
}

// ClinicalNoteContext atiende GET
// /internal/appointments/:id/clinical-note-context.
func (h *SchedulingHandler) ClinicalNoteContext(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	result, err := h.scheduling.ClinicalNoteContext(c.Request.Context(), actor, c.Param("id"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, result)
}
