package agenda

import (
	"context"
	"strings"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/ids"
)

// Formato de las fechas en la API, y límite del rango del conteo (RF21).
const (
	reportDateLayout = "2006-01-02"
	maxReportDays    = 366
)

// Estados de una cita. Coinciden con el CHECK de la migración 0007.
const (
	StatusBooked      = "booked"
	StatusAttended    = "attended"
	StatusNoShow      = "no_show"
	StatusCancelled   = "cancelled"
	StatusRescheduled = "rescheduled"
)

// validStatuses permite validar el filtro de estado de la agenda.
var validStatuses = map[string]struct{}{
	StatusBooked:      {},
	StatusAttended:    {},
	StatusNoShow:      {},
	StatusCancelled:   {},
	StatusRescheduled: {},
}

// AgendaPatient es el paciente de una cita en la agenda. A propósito no lleva
// datos de contacto: así ver la agenda no cuenta como leer contacto (RF18) y no
// genera eventos de auditoría.
type AgendaPatient struct {
	ID             string `json:"id"`
	Identification string `json:"identification"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
}

// AgendaPractitioner es el profesional de una cita en la agenda.
type AgendaPractitioner struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// AgendaService es el servicio de una cita en la agenda.
type AgendaService struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
}

// AgendaEntry es una cita dentro de la agenda del día (RF20).
type AgendaEntry struct {
	ID string `json:"id"`
	// Las horas salen en la zona horaria de la clínica.
	StartsAt     time.Time          `json:"starts_at"`
	EndsAt       time.Time          `json:"ends_at"`
	Status       string             `json:"status"`
	Patient      AgendaPatient      `json:"patient"`
	Practitioner AgendaPractitioner `json:"practitioner"`
	Service      AgendaService      `json:"service"`
}

// DayAgenda es la agenda de un día. PractitionerID va vacío cuando la agenda es
// la de toda la clínica.
type DayAgenda struct {
	Date           string        `json:"date"`
	Timezone       string        `json:"timezone"`
	PractitionerID string        `json:"practitioner_id,omitempty"`
	Items          []AgendaEntry `json:"items"`
}

// AgendaQuery son los parámetros de la agenda del día.
type AgendaQuery struct {
	// Date va en formato AAAA-MM-DD y se interpreta en la zona de la clínica.
	Date string
	// PractitionerID es opcional. Vacío significa toda la clínica, salvo para
	// un profesional, que siempre ve solo la suya.
	PractitionerID string
	// Status es un filtro opcional por estado de la cita.
	Status string
}

// StatusCounts es el número de citas en cada estado. Todos los estados salen
// siempre, con cero cuando no hay citas.
type StatusCounts struct {
	Booked      int `json:"booked"`
	Attended    int `json:"attended"`
	NoShow      int `json:"no_show"`
	Cancelled   int `json:"cancelled"`
	Rescheduled int `json:"rescheduled"`
}

// StatusReport es el conteo de citas por estado en un rango de fechas (RF21).
type StatusReport struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Timezone string       `json:"timezone"`
	Counts   StatusCounts `json:"counts"`
	Total    int          `json:"total"`
}

// ReportStore consulta las citas de la clínica. Cada método recibe la
// transacción en la que debe correr, y solo ve filas de la clínica recibida
// (I5).
type ReportStore interface {
	// ClinicTimezone devuelve la zona horaria de la clínica.
	ClinicTimezone(ctx context.Context, db database.DBTX, clinicID string) (string, error)
	// PractitionerIDBySubject devuelve el profesional que corresponde a un
	// usuario, o un not found si el usuario no tiene perfil de profesional.
	PractitionerIDBySubject(ctx context.Context, db database.DBTX, clinicID, subject string) (string, error)
	// PractitionerExists indica si el profesional existe en la clínica.
	PractitionerExists(ctx context.Context, db database.DBTX, clinicID, practitionerID string) (bool, error)
	// Agenda devuelve las citas del día, ordenadas por hora de inicio.
	Agenda(ctx context.Context, db database.DBTX, clinicID, timezone string, query AgendaQuery) ([]AgendaEntry, error)
	// CountByStatus cuenta las citas cuya hora de inicio cae entre las dos
	// fechas, ambas incluidas.
	CountByStatus(ctx context.Context, db database.DBTX, clinicID, timezone, from, to string) (map[string]int, error)
}

// ReportTxRunner lo implementa *database.TxManager.
type ReportTxRunner interface {
	Run(
		ctx context.Context,
		opts database.TxOptions,
		fn func(context.Context, *database.Tx) error,
	) error
}

// Reports implementa la agenda del día (RF20) y el conteo por estado (RF21).
type Reports struct {
	store ReportStore
	tx    ReportTxRunner
}

// NewReports devuelve los casos de uso de agenda y reportes.
func NewReports(store ReportStore, tx ReportTxRunner) *Reports {
	return &Reports{store: store, tx: tx}
}

// Agenda devuelve las citas de un día (RF20).
//
// Con un profesional devuelve la agenda de ese profesional, y sin él la de toda
// la clínica, que es la vista de recepción. Un profesional solo puede ver la
// suya: se le aplica su propio perfil aunque no envíe el parámetro, y si pide
// la de otro responde 403. El día se interpreta en la zona de la clínica.
// Como no se leen datos de contacto, no se audita y la transacción es de solo
// lectura.
func (r *Reports) Agenda(ctx context.Context, actor auth.Actor, query AgendaQuery) (DayAgenda, error) {
	query.Date = strings.TrimSpace(query.Date)
	query.PractitionerID = strings.TrimSpace(query.PractitionerID)
	query.Status = strings.TrimSpace(query.Status)

	if _, err := time.Parse(reportDateLayout, query.Date); err != nil {
		return DayAgenda{}, apperr.Invalid("date is required and must have the format YYYY-MM-DD")
	}

	if query.Status != "" {
		if _, ok := validStatuses[query.Status]; !ok {
			return DayAgenda{}, apperr.Invalid("status must be booked, attended, no_show, cancelled or rescheduled")
		}
	}

	if query.PractitionerID != "" {
		normalized, ok := ids.Normalize(query.PractitionerID)
		if !ok {
			return DayAgenda{}, apperr.Invalid("invalid practitioner id")
		}

		query.PractitionerID = normalized
	}

	var agenda DayAgenda

	err := r.tx.Run(ctx, database.TxOptions{ClinicID: actor.ClinicID, ReadOnly: true}, func(ctx context.Context, tx *database.Tx) error {
		timezone, err := r.store.ClinicTimezone(ctx, tx, actor.ClinicID)
		if err != nil {
			return err
		}

		practitionerID, err := r.resolvePractitioner(ctx, tx, actor, query.PractitionerID)
		if err != nil {
			return err
		}

		query.PractitionerID = practitionerID

		items, err := r.store.Agenda(ctx, tx, actor.ClinicID, timezone, query)
		if err != nil {
			return err
		}

		agenda = DayAgenda{
			Date:           query.Date,
			Timezone:       timezone,
			PractitionerID: practitionerID,
			Items:          items,
		}

		return nil
	})
	if err != nil {
		return DayAgenda{}, err
	}

	agenda.Items, err = inClinicZone(agenda.Items, agenda.Timezone)
	if err != nil {
		return DayAgenda{}, err
	}

	return agenda, nil
}

// resolvePractitioner decide de qué profesional es la agenda.
//
// Un profesional queda limitado a su propio perfil. Para los demás roles, un
// profesional pedido debe existir en la clínica: uno de otra clínica responde
// 404, igual que un id inexistente.
func (r *Reports) resolvePractitioner(
	ctx context.Context,
	db database.DBTX,
	actor auth.Actor,
	requested string,
) (string, error) {
	if actor.Role == auth.RolePractitioner {
		own, err := r.store.PractitionerIDBySubject(ctx, db, actor.ClinicID, actor.Subject)
		if err != nil {
			return "", err
		}

		if requested != "" && requested != own {
			return "", apperr.Forbidden("a practitioner can only see their own agenda")
		}

		return own, nil
	}

	if requested == "" {
		return "", nil
	}

	exists, err := r.store.PractitionerExists(ctx, db, actor.ClinicID, requested)
	if err != nil {
		return "", err
	}

	if !exists {
		return "", apperr.NotFound("practitioner not found")
	}

	return requested, nil
}

// CountByStatus cuenta las citas por estado en un rango de fechas (RF21).
//
// Las dos fechas se incluyen, se interpretan en la zona de la clínica y se
// comparan con la hora de inicio de la cita. Es una sola consulta agregada.
func (r *Reports) CountByStatus(ctx context.Context, actor auth.Actor, from, to string) (StatusReport, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)

	fromDate, err := time.Parse(reportDateLayout, from)
	if err != nil {
		return StatusReport{}, apperr.Invalid("from is required and must have the format YYYY-MM-DD")
	}

	toDate, err := time.Parse(reportDateLayout, to)
	if err != nil {
		return StatusReport{}, apperr.Invalid("to is required and must have the format YYYY-MM-DD")
	}

	if toDate.Before(fromDate) {
		return StatusReport{}, apperr.Invalid("to must not be before from")
	}

	// El rango incluye los dos extremos, así que se suma un día.
	if int(toDate.Sub(fromDate).Hours()/24)+1 > maxReportDays {
		return StatusReport{}, apperr.Invalid("the range must not exceed 366 days")
	}

	var report StatusReport

	err = r.tx.Run(ctx, database.TxOptions{ClinicID: actor.ClinicID, ReadOnly: true}, func(ctx context.Context, tx *database.Tx) error {
		timezone, err := r.store.ClinicTimezone(ctx, tx, actor.ClinicID)
		if err != nil {
			return err
		}

		counts, err := r.store.CountByStatus(ctx, tx, actor.ClinicID, timezone, from, to)
		if err != nil {
			return err
		}

		report = buildStatusReport(from, to, timezone, counts)

		return nil
	})
	if err != nil {
		return StatusReport{}, err
	}

	return report, nil
}

// buildStatusReport arma el reporte con todos los estados, también los que no
// tienen citas.
func buildStatusReport(from, to, timezone string, counts map[string]int) StatusReport {
	report := StatusReport{
		From:     from,
		To:       to,
		Timezone: timezone,
		Counts: StatusCounts{
			Booked:      counts[StatusBooked],
			Attended:    counts[StatusAttended],
			NoShow:      counts[StatusNoShow],
			Cancelled:   counts[StatusCancelled],
			Rescheduled: counts[StatusRescheduled],
		},
	}

	report.Total = report.Counts.Booked + report.Counts.Attended +
		report.Counts.NoShow + report.Counts.Cancelled + report.Counts.Rescheduled

	return report
}

// inClinicZone convierte las horas de las citas a la zona horaria de la clínica,
// para que quien lea la respuesta vea la hora de pared de la clínica y no la
// del servidor.
func inClinicZone(items []AgendaEntry, timezone string) ([]AgendaEntry, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}

	for i := range items {
		items[i].StartsAt = items[i].StartsAt.In(location)
		items[i].EndsAt = items[i].EndsAt.In(location)
	}

	return items, nil
}
