package agenda

import (
	"log/slog"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/httpx"
	"github.com/gin-gonic/gin"
)

// ReportHandler expone la agenda del día (RF20) y el conteo de citas por
// estado (RF21) por HTTP.
type ReportHandler struct {
	reports *Reports
	logger  *slog.Logger
}

// NewReportHandler devuelve el handler HTTP de agenda y reportes.
func NewReportHandler(reports *Reports, logger *slog.Logger) *ReportHandler {
	return &ReportHandler{reports: reports, logger: logger}
}

// RegisterRoutes agrega las rutas a api, que ya exige un token válido. La
// agenda la ven administración, recepción y los profesionales (cada uno solo la
// suya). El conteo por estado es de administración.
func (h *ReportHandler) RegisterRoutes(api *gin.RouterGroup) {
	agenda := auth.RequirePermission(auth.PermAgendaRead)
	reports := auth.RequirePermission(auth.PermReportsRead)

	api.GET("/agenda", agenda, h.GetAgenda)
	api.GET("/reports/appointments-by-status", reports, h.CountByStatus)
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
