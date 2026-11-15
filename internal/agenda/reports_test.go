package agenda

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
)

const (
	ownPractitionerID   = "c1c1c1c1-0000-4000-8000-000000000001"
	otherPractitionerID = "c1c1c1c1-0000-4000-8000-000000000002"
)

// reportsAdmin, reportsReceptionist y reportsPractitioner son los actores de
// las pruebas. Los tres son de la clínica A.
var (
	reportsAdmin        = auth.Actor{Subject: "admin-subject", ClinicID: clinicA, Role: auth.RoleClinicAdmin}
	reportsReceptionist = auth.Actor{Subject: "reception-subject", ClinicID: clinicA, Role: auth.RoleReception}
	reportsPractitioner = auth.Actor{Subject: "practitioner-subject", ClinicID: clinicA, Role: auth.RolePractitioner}
)

// fakeReportStore registra las llamadas que recibe. El SQL real se prueba
// contra la base de datos, no aquí.
type fakeReportStore struct {
	timezone string
	// ownID es el profesional que corresponde al usuario que consulta.
	ownID string
	// exists es lo que responde PractitionerExists.
	exists  bool
	entries []AgendaEntry
	counts  map[string]int
	err     error
	// subjectErr es el error de PractitionerIDBySubject, aparte de err para
	// probar solo ese paso.
	subjectErr error

	clinicIDs    []string
	agendaQuery  AgendaQuery
	agendaCalls  int
	countRange   [2]string
	countCalls   int
	existsCalls  int
	subjectCalls int
}

func (s *fakeReportStore) ClinicTimezone(_ context.Context, _ database.DBTX, clinicID string) (string, error) {
	s.clinicIDs = append(s.clinicIDs, clinicID)

	return s.timezone, s.err
}

func (s *fakeReportStore) PractitionerIDBySubject(_ context.Context, _ database.DBTX, clinicID, _ string) (string, error) {
	s.subjectCalls++
	s.clinicIDs = append(s.clinicIDs, clinicID)

	return s.ownID, s.subjectErr
}

func (s *fakeReportStore) PractitionerExists(_ context.Context, _ database.DBTX, clinicID, _ string) (bool, error) {
	s.existsCalls++
	s.clinicIDs = append(s.clinicIDs, clinicID)

	return s.exists, s.err
}

func (s *fakeReportStore) Agenda(_ context.Context, _ database.DBTX, clinicID, _ string, query AgendaQuery) ([]AgendaEntry, error) {
	s.agendaCalls++
	s.agendaQuery = query
	s.clinicIDs = append(s.clinicIDs, clinicID)

	return s.entries, s.err
}

func (s *fakeReportStore) CountByStatus(_ context.Context, _ database.DBTX, clinicID, _, from, to string) (map[string]int, error) {
	s.countCalls++
	s.countRange = [2]string{from, to}
	s.clinicIDs = append(s.clinicIDs, clinicID)

	return s.counts, s.err
}

// newTestReports arma el servicio con sus fakes. Reutiliza el fake de
// transacciones de pacientes, que solo registra las opciones y ejecuta la
// función.
func newTestReports() (*Reports, *fakeReportStore, *fakePatientTx) {
	store := &fakeReportStore{
		timezone: "America/Costa_Rica",
		ownID:    ownPractitionerID,
		exists:   true,
		entries:  []AgendaEntry{},
		counts:   map[string]int{},
	}
	tx := &fakePatientTx{}

	return NewReports(store, tx), store, tx
}

// Cubre RF20 (agenda del día)
//
// Las entradas inválidas se rechazan antes de abrir la transacción: nada llega
// a la base de datos.
func TestAgendaValidation(t *testing.T) {
	tests := []struct {
		name  string
		query AgendaQuery
	}{
		{"sin fecha", AgendaQuery{}},
		{"fecha con otro formato", AgendaQuery{Date: "16/11/2026"}},
		{"fecha inexistente", AgendaQuery{Date: "2026-02-31"}},
		{"estado desconocido", AgendaQuery{Date: "2026-11-16", Status: "pendiente"}},
		{"profesional con formato inválido", AgendaQuery{Date: "2026-11-16", PractitionerID: "no-es-un-uuid"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reports, store, tx := newTestReports()

			_, err := reports.Agenda(context.Background(), reportsReceptionist, tt.query)
			if !errors.Is(err, apperr.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}

			if len(tx.options) != 0 || store.agendaCalls != 0 {
				t.Error("una entrada inválida no debe abrir transacción ni consultar")
			}
		})
	}
}

// Cubre RF20 (agenda de toda la clínica para recepción)
// Cubre I5 (la clínica sale del actor)
//
// Sin profesional, la agenda es la de toda la clínica del actor, en una
// transacción de solo lectura que fija esa clínica.
func TestAgendaWithoutPractitionerIsClinicWideAndReadOnly(t *testing.T) {
	reports, store, tx := newTestReports()

	agenda, err := reports.Agenda(context.Background(), reportsReceptionist, AgendaQuery{Date: "2026-11-16"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.agendaQuery.PractitionerID != "" || agenda.PractitionerID != "" {
		t.Errorf("la agenda debe ser de toda la clínica, practitioner = %q", store.agendaQuery.PractitionerID)
	}

	if store.existsCalls != 0 {
		t.Error("sin profesional pedido no hay nada que comprobar")
	}

	if len(tx.options) != 1 || !tx.options[0].ReadOnly || tx.options[0].ClinicID != clinicA {
		t.Errorf("opciones de la transacción = %+v, want solo lectura con la clínica del actor", tx.options)
	}

	for _, id := range store.clinicIDs {
		if id != clinicA {
			t.Errorf("el almacén recibió la clínica %q, want %q", id, clinicA)
		}
	}

	if agenda.Items == nil {
		t.Error("sin citas debe devolver una lista vacía, no nula")
	}
}

// Cubre RF20 (agenda de un profesional)
// Cubre I5 (un profesional de otra clínica no existe)
//
// Un profesional pedido por recepción debe existir en la clínica. Si no existe,
// o es de otra clínica, responde 404 sin consultar las citas.
func TestAgendaOfAnUnknownPractitionerIsNotFound(t *testing.T) {
	reports, store, _ := newTestReports()
	store.exists = false

	_, err := reports.Agenda(context.Background(), reportsReceptionist, AgendaQuery{
		Date:           "2026-11-16",
		PractitionerID: otherPractitionerID,
	})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}

	if store.agendaCalls != 0 {
		t.Error("no debe consultar citas de un profesional que no existe en la clínica")
	}
}

// Cubre RF20 (agenda de un profesional)
//
// Con un profesional que existe, la consulta se acota a él.
func TestAgendaOfAPractitionerIsScopedToThem(t *testing.T) {
	reports, store, _ := newTestReports()

	agenda, err := reports.Agenda(context.Background(), reportsAdmin, AgendaQuery{
		Date:           "2026-11-16",
		PractitionerID: strings.ToUpper(otherPractitionerID),
		Status:         StatusBooked,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.agendaQuery.PractitionerID != otherPractitionerID || agenda.PractitionerID != otherPractitionerID {
		t.Errorf("practitioner = %q, want %q normalizado", store.agendaQuery.PractitionerID, otherPractitionerID)
	}

	if store.agendaQuery.Status != StatusBooked {
		t.Errorf("status = %q, want %q", store.agendaQuery.Status, StatusBooked)
	}
}

// Cubre RF20 (el profesional consulta su propia agenda)
//
// Un profesional que no envía el parámetro recibe su propia agenda, no la de
// toda la clínica.
func TestAgendaOfAPractitionerDefaultsToTheirOwn(t *testing.T) {
	reports, store, _ := newTestReports()

	agenda, err := reports.Agenda(context.Background(), reportsPractitioner, AgendaQuery{Date: "2026-11-16"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.agendaQuery.PractitionerID != ownPractitionerID || agenda.PractitionerID != ownPractitionerID {
		t.Errorf("practitioner = %q, want la agenda propia %q", store.agendaQuery.PractitionerID, ownPractitionerID)
	}

	if store.existsCalls != 0 {
		t.Error("el profesional no necesita comprobar la existencia de su propio perfil")
	}
}

// Cubre RF20 (el profesional consulta su propia agenda)
//
// Un profesional no puede ver la agenda de otro, y pedir la suya es válido.
func TestAgendaOfAnotherPractitionerIsForbidden(t *testing.T) {
	reports, store, _ := newTestReports()

	_, err := reports.Agenda(context.Background(), reportsPractitioner, AgendaQuery{
		Date:           "2026-11-16",
		PractitionerID: otherPractitionerID,
	})
	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}

	if store.agendaCalls != 0 {
		t.Error("no debe consultar la agenda de otro profesional")
	}

	if _, err := reports.Agenda(context.Background(), reportsPractitioner, AgendaQuery{
		Date:           "2026-11-16",
		PractitionerID: ownPractitionerID,
	}); err != nil {
		t.Errorf("pedir la agenda propia debe funcionar, error = %v", err)
	}
}

// Cubre RF20 (un usuario sin perfil de profesional no ve agenda)
func TestAgendaOfAUserWithoutPractitionerProfileFails(t *testing.T) {
	reports, store, _ := newTestReports()
	store.subjectErr = apperr.NotFound("practitioner not found")

	_, err := reports.Agenda(context.Background(), reportsPractitioner, AgendaQuery{Date: "2026-11-16"})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}

	if store.agendaCalls != 0 {
		t.Error("sin perfil de profesional no debe consultar citas")
	}
}

// Cubre RF20 (el día se interpreta en la zona de la clínica)
//
// Las horas salen en la zona de la clínica: 14:00 UTC es 09:00 en Bogotá.
func TestAgendaReturnsTimesInTheClinicTimezone(t *testing.T) {
	reports, store, _ := newTestReports()
	store.timezone = "America/Bogota"

	start := time.Date(2026, time.November, 16, 14, 0, 0, 0, time.UTC)
	store.entries = []AgendaEntry{{ID: "x", StartsAt: start, EndsAt: start.Add(30 * time.Minute)}}

	agenda, err := reports.Agenda(context.Background(), reportsAdmin, AgendaQuery{Date: "2026-11-16"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if agenda.Timezone != "America/Bogota" {
		t.Errorf("timezone = %q, want America/Bogota", agenda.Timezone)
	}

	got := agenda.Items[0].StartsAt
	if got.Hour() != 9 || !got.Equal(start) {
		t.Errorf("starts_at = %v, want las 09:00 de Bogotá (el mismo instante)", got)
	}
}

// Cubre RF20 (errores de la base de datos)
func TestAgendaPropagatesStoreErrors(t *testing.T) {
	reports, store, _ := newTestReports()
	store.err = errors.New("boom")

	if _, err := reports.Agenda(context.Background(), reportsAdmin, AgendaQuery{Date: "2026-11-16"}); err == nil {
		t.Fatal("un error del almacén debe llegar al llamador")
	}
}

// Cubre RF20 y RF18 (la agenda no expone datos de contacto)
//
// El paciente de la agenda no tiene teléfono, correo ni dirección. Si algún día
// se agregaran, la lectura tendría que auditarse (RF18).
func TestAgendaPatientHasNoContactFields(t *testing.T) {
	contact := map[string]bool{"phone": true, "email": true, "address": true}

	patient := reflect.TypeOf(AgendaPatient{})
	for i := 0; i < patient.NumField(); i++ {
		name := strings.Split(patient.Field(i).Tag.Get("json"), ",")[0]
		if contact[name] {
			t.Errorf("AgendaPatient expone %q: los datos de contacto no deben salir en la agenda", name)
		}
	}
}

// Cubre RF21 (conteo por estado)
// Cubre I5 (la clínica sale del actor)
//
// El conteo incluye siempre los cinco estados, con cero cuando no hay citas, y
// suma el total.
func TestCountByStatusFillsEveryStatusAndTheTotal(t *testing.T) {
	reports, store, tx := newTestReports()
	store.counts = map[string]int{StatusBooked: 9, StatusAttended: 4, StatusCancelled: 1}

	report, err := reports.CountByStatus(context.Background(), reportsAdmin, "2026-11-09", "2026-11-20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := StatusCounts{Booked: 9, Attended: 4, NoShow: 0, Cancelled: 1, Rescheduled: 0}
	if report.Counts != want || report.Total != 14 {
		t.Errorf("counts = %+v total = %d, want %+v total 14", report.Counts, report.Total, want)
	}

	if report.From != "2026-11-09" || report.To != "2026-11-20" || report.Timezone != "America/Costa_Rica" {
		t.Errorf("reporte = %+v", report)
	}

	if store.countRange != [2]string{"2026-11-09", "2026-11-20"} {
		t.Errorf("rango enviado al almacén = %v", store.countRange)
	}

	if len(tx.options) != 1 || !tx.options[0].ReadOnly || tx.options[0].ClinicID != clinicA {
		t.Errorf("opciones de la transacción = %+v, want solo lectura con la clínica del actor", tx.options)
	}

	for _, id := range store.clinicIDs {
		if id != clinicA {
			t.Errorf("el almacén recibió la clínica %q, want %q", id, clinicA)
		}
	}
}

// Cubre RF21 (rango de fechas)
//
// Un rango inválido se rechaza sin consultar. Un día solo (from igual a to) y
// 366 días son válidos; 367 no.
func TestCountByStatusValidatesTheRange(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		valid    bool
	}{
		{"sin from", "", "2026-11-20", false},
		{"sin to", "2026-11-09", "", false},
		{"from con otro formato", "09-11-2026", "2026-11-20", false},
		{"to con otro formato", "2026-11-09", "20/11/2026", false},
		{"to anterior a from", "2026-11-20", "2026-11-09", false},
		{"un solo día", "2026-11-16", "2026-11-16", true},
		{"366 días", "2026-01-01", "2027-01-01", true},
		{"367 días", "2026-01-01", "2027-01-02", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reports, store, _ := newTestReports()

			_, err := reports.CountByStatus(context.Background(), reportsAdmin, tt.from, tt.to)

			if tt.valid {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			if !errors.Is(err, apperr.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}

			if store.countCalls != 0 {
				t.Error("un rango inválido no debe consultar")
			}
		})
	}
}

// Cubre RF21 (errores de la base de datos)
func TestCountByStatusPropagatesStoreErrors(t *testing.T) {
	reports, store, _ := newTestReports()
	store.err = errors.New("boom")

	if _, err := reports.CountByStatus(context.Background(), reportsAdmin, "2026-11-09", "2026-11-20"); err == nil {
		t.Fatal("un error del almacén debe llegar al llamador")
	}
}

// Cubre RF20 y RF21 (permisos)
//
// La agenda es para administración, recepción y profesionales. El conteo por
// estado es solo de administración, porque no está en la matriz del enunciado y
// es un reporte de gestión.
func TestReportPermissionsPerRole(t *testing.T) {
	tests := []struct {
		role   auth.Role
		agenda bool
		report bool
	}{
		{auth.RoleClinicAdmin, true, true},
		{auth.RoleReception, true, false},
		{auth.RolePractitioner, true, false},
		{auth.RolePlatformAdmin, false, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := auth.RoleHasPermission(tt.role, auth.PermAgendaRead); got != tt.agenda {
				t.Errorf("agenda:read = %v, want %v", got, tt.agenda)
			}

			if got := auth.RoleHasPermission(tt.role, auth.PermReportsRead); got != tt.report {
				t.Errorf("reports:read = %v, want %v", got, tt.report)
			}
		})
	}
}
