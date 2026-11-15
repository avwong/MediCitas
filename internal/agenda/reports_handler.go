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

// ReportHandler expone el historial del paciente (RF16), la agenda del día
// (RF20) y el conteo de citas por estado (RF21) por HTTP.
type ReportHandler struct {
	reports *Reports
	logger  *slog.Logger
}

// NewReportHandler devuelve el handler HTTP de agenda y reportes.
func NewReportHandler(reports *Reports, logger *slog.Logger) *ReportHandler {
	return &ReportHandler{reports: reports, logger: logger}
}

// RegisterRoutes agrega las rutas a api, que ya exige un token válido. El
// historial lo leen administración, recepción y profesionales, según la matriz
// del enunciado. La agenda la ven los mismos tres roles (cada profesional solo
// la suya). El conteo por estado es de administración.
func (h *ReportHandler) RegisterRoutes(api *gin.RouterGroup) {
	history := auth.RequirePermission(auth.PermHistoryRead)
	agenda := auth.RequirePermission(auth.PermAgendaRead)
	reports := auth.RequirePermission(auth.PermReportsRead)

	api.GET("/patients/:id/history", history, h.GetHistory)

	api.GET("/agenda", agenda, h.GetAgenda)
	api.GET("/reports/appointments-by-status", reports, h.CountByStatus)
}

// GetHistory atiende GET /api/v1/patients/:id/history. Recibe limit, opcional.
func (h *ReportHandler) GetHistory(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	// El límite es opcional. Si viene debe ser un entero; el rango lo valida
	// el servicio.
	limit := 0

	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			httpx.WriteError(c, h.logger, apperr.Invalid("limit must be an integer"))
			return
		}

		limit = parsed
	}

	history, err := h.reports.History(c.Request.Context(), actor, c.Param("id"), limit)
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, history)
}

// GetAgenda atiende GET /api/v1/agenda. Recibe date (obligatorio, AAAA-MM-DD),
// practitioner_id (opcional) y status (opcional). La clínica sale siempre del
// token, nunca de los parámetros (I5).
func (h *ReportHandler) GetAgenda(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	agenda, err := h.reports.Agenda(c.Request.Context(), actor, AgendaQuery{
		Date:           c.Query("date"),
		PractitionerID: c.Query("practitioner_id"),
		Status:         c.Query("status"),
	})
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, agenda)
}

// CountByStatus atiende GET /api/v1/reports/appointments-by-status. Recibe from
// y to, ambos obligatorios y en formato AAAA-MM-DD.
func (h *ReportHandler) CountByStatus(c *gin.Context) {
	actor, ok := auth.CurrentActor(c)
	if !ok {
		return
	}

	report, err := h.reports.CountByStatus(c.Request.Context(), actor, c.Query("from"), c.Query("to"))
	if err != nil {
		httpx.WriteError(c, h.logger, err)
		return
	}

	c.JSON(http.StatusOK, report)
}
