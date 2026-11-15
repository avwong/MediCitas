package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Las pruebas de RF20 y RF21 se apoyan en las citas de seed/agenda.sql. Sus
// identificadores son fijos: a1a1a1a1 es Demo, a2a2a2a2 es Norte y a3a3a3a3 es
// Sur, seguidos del número de la cita.
const (
	demoPrefix  = "a1a1a1a1"
	northPrefix = "a2a2a2a2"
	southPrefix = "a3a3a3a3"

	demoPractitioner1  = "c1c1c1c1-0000-4000-8000-000000000001"
	demoPractitioner2  = "c1c1c1c1-0000-4000-8000-000000000002"
	northPractitioner1 = "c2c2c2c2-0000-4000-8000-000000000001"

	// monday es el lunes 16 de noviembre de 2026, el día con más citas
	// reservadas de la semilla.
	monday = "2026-11-16"
)

// appointmentID arma el identificador de una cita de la semilla.
func appointmentID(prefix string, number int) string {
	return fmt.Sprintf("%s-0000-4000-8000-%012d", prefix, number)
}

// agendaItem es una cita en la respuesta de la agenda. El paciente se lee como
// mapa para poder comprobar qué campos trae.
type agendaItem struct {
	ID           string         `json:"id"`
	StartsAt     string         `json:"starts_at"`
	Status       string         `json:"status"`
	Patient      map[string]any `json:"patient"`
	Practitioner struct {
		ID string `json:"id"`
	} `json:"practitioner"`
}

type agendaResponse struct {
	Date           string       `json:"date"`
	Timezone       string       `json:"timezone"`
	PractitionerID string       `json:"practitioner_id"`
	Items          []agendaItem `json:"items"`
}

type countsResponse struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Timezone string         `json:"timezone"`
	Counts   map[string]int `json:"counts"`
	Total    int            `json:"total"`
}

// getAgenda pide la agenda y espera un 200.
func (s *suite) getAgenda(ctx context.Context, token, query string) (agendaResponse, error) {
	var agenda agendaResponse

	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/agenda?"+query, token, "", http.StatusOK)
	if err != nil {
		return agenda, err
	}

	if err := json.Unmarshal(raw, &agenda); err != nil {
		return agenda, fmt.Errorf("respuesta de la agenda: %w", err)
	}

	return agenda, nil
}

// getCounts pide el conteo por estado con el token de quien consulta y espera
// un 200.
func (s *suite) getCounts(ctx context.Context, token, query string) (countsResponse, error) {
	var counts countsResponse

	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/reports/appointments-by-status?"+query, token, "", http.StatusOK)
	if err != nil {
		return counts, err
	}

	if err := json.Unmarshal(raw, &counts); err != nil {
		return counts, fmt.Errorf("respuesta del conteo: %w", err)
	}

	return counts, nil
}

func (a agendaResponse) ids() []string {
	ids := make([]string, 0, len(a.Items))
	for _, item := range a.Items {
		ids = append(ids, item.ID)
	}

	return ids
}

// mustContain falla si falta alguno de los identificadores esperados.
func mustContain(ids []string, wanted ...string) error {
	for _, id := range wanted {
		if !contains(ids, id) {
			return fmt.Errorf("falta la cita %s en %v", id, ids)
		}
	}

	return nil
}

// mustNotContain falla si aparece alguno de los identificadores.
func mustNotContain(ids []string, unwanted ...string) error {
	for _, id := range unwanted {
		if contains(ids, id) {
			return fmt.Errorf("no debía aparecer la cita %s", id)
		}
	}

	return nil
}

func (s *suite) agendaClinicWide(ctx context.Context) error {
	expected := []string{
		appointmentID(demoPrefix, 8), appointmentID(demoPrefix, 9),
		appointmentID(demoPrefix, 10), appointmentID(demoPrefix, 11),
	}

	for _, token := range []string{s.reception, s.admin} {
		agenda, err := s.getAgenda(ctx, token, "date="+monday)
		if err != nil {
			return err
		}

		if agenda.PractitionerID != "" {
			return fmt.Errorf("practitioner_id = %q, want vacío: es la agenda de toda la clínica", agenda.PractitionerID)
		}

		if err := mustContain(agenda.ids(), expected...); err != nil {
			return err
		}

		// Las citas salen ordenadas por hora de inicio. Todas llevan la misma
		// zona, así que basta comparar el texto.
		for i := 1; i < len(agenda.Items); i++ {
			if agenda.Items[i].StartsAt < agenda.Items[i-1].StartsAt {
				return fmt.Errorf("la agenda no está ordenada por hora: %s antes de %s", agenda.Items[i-1].StartsAt, agenda.Items[i].StartsAt)
			}
		}

		// El paciente trae nombre e identificación, nunca contacto.
		for _, item := range agenda.Items {
			for _, field := range []string{"phone", "email", "address"} {
				if _, found := item.Patient[field]; found {
					return fmt.Errorf("la agenda expone el campo de contacto %q", field)
				}
			}

			if item.Patient["identification"] == nil || item.Patient["first_name"] == nil {
				return fmt.Errorf("el paciente de la cita %s no trae nombre ni identificación", item.ID)
			}
		}
	}

	return nil
}

func (s *suite) agendaOfPractitioner(ctx context.Context) error {
	agenda, err := s.getAgenda(ctx, s.reception, "date="+monday+"&practitioner_id="+demoPractitioner1)
	if err != nil {
		return err
	}

	if agenda.PractitionerID != demoPractitioner1 {
		return fmt.Errorf("practitioner_id = %q, want %q", agenda.PractitionerID, demoPractitioner1)
	}

	if err := mustContain(agenda.ids(), appointmentID(demoPrefix, 8), appointmentID(demoPrefix, 9), appointmentID(demoPrefix, 10)); err != nil {
		return err
	}

	// La cita 11 es de otro profesional de la misma clínica.
	if err := mustNotContain(agenda.ids(), appointmentID(demoPrefix, 11)); err != nil {
		return err
	}

	for _, item := range agenda.Items {
		if item.Practitioner.ID != demoPractitioner1 {
			return fmt.Errorf("la cita %s es del profesional %s, no del pedido", item.ID, item.Practitioner.ID)
		}
	}

	return nil
}

func (s *suite) agendaStatusFilter(ctx context.Context) error {
	// El 11 de noviembre tiene una cita atendida (3) y una cancelada (4).
	all, err := s.getAgenda(ctx, s.reception, "date=2026-11-11")
	if err != nil {
		return err
	}

	if err := mustContain(all.ids(), appointmentID(demoPrefix, 3), appointmentID(demoPrefix, 4)); err != nil {
		return err
	}

	cases := []struct {
		status  string
		wanted  string
		missing string
	}{
		{"attended", appointmentID(demoPrefix, 3), appointmentID(demoPrefix, 4)},
		{"cancelled", appointmentID(demoPrefix, 4), appointmentID(demoPrefix, 3)},
	}

	for _, tc := range cases {
		filtered, err := s.getAgenda(ctx, s.reception, "date=2026-11-11&status="+tc.status)
		if err != nil {
			return err
		}

		if err := mustContain(filtered.ids(), tc.wanted); err != nil {
			return err
		}

		if err := mustNotContain(filtered.ids(), tc.missing); err != nil {
			return err
		}

		for _, item := range filtered.Items {
			if item.Status != tc.status {
				return fmt.Errorf("el filtro %s devolvió una cita %s", tc.status, item.Status)
			}
		}
	}

	return nil
}

func (s *suite) practitionerSeesOnlyOwnAgenda(ctx context.Context) error {
	// Sin parámetro, cada profesional recibe su propia agenda.
	first, err := s.getAgenda(ctx, s.practitioner, "date="+monday)
	if err != nil {
		return err
	}

	if first.PractitionerID != demoPractitioner1 {
		return fmt.Errorf("practitioner_id = %q, want la agenda propia %q", first.PractitionerID, demoPractitioner1)
	}

	if err := mustContain(first.ids(), appointmentID(demoPrefix, 8), appointmentID(demoPrefix, 9), appointmentID(demoPrefix, 10)); err != nil {
		return err
	}

	if err := mustNotContain(first.ids(), appointmentID(demoPrefix, 11)); err != nil {
		return err
	}

	second, err := s.getAgenda(ctx, s.practitioner2, "date="+monday)
	if err != nil {
		return err
	}

	if err := mustContain(second.ids(), appointmentID(demoPrefix, 11)); err != nil {
		return err
	}

	if err := mustNotContain(second.ids(), appointmentID(demoPrefix, 8)); err != nil {
		return err
	}

	// Pedir la agenda de otro profesional de la misma clínica es 403, y pedir
	// la propia es válido.
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/agenda?date="+monday+"&practitioner_id="+demoPractitioner2, s.practitioner, "", http.StatusForbidden); err != nil {
		return err
	}

	_, err = s.expect(ctx, http.MethodGet, "/api/v1/agenda?date="+monday+"&practitioner_id="+demoPractitioner1, s.practitioner, "", http.StatusOK)

	return err
}

func (s *suite) agendaInvalidInputs(ctx context.Context) error {
	invalid := []string{
		"",
		"date=",
		"date=16/11/2026",
		"date=2026-02-31",
		"date=" + monday + "&status=pendiente",
		"date=" + monday + "&practitioner_id=no-es-un-uuid",
	}

	for _, query := range invalid {
		if _, err := s.expect(ctx, http.MethodGet, "/api/v1/agenda?"+query, s.reception, "", http.StatusBadRequest); err != nil {
			return err
		}
	}

	// Un profesional con formato válido pero que no existe en la clínica.
	_, err := s.expect(ctx, http.MethodGet, "/api/v1/agenda?date="+monday+"&practitioner_id="+unknownPatientID, s.reception, "", http.StatusNotFound)

	return err
}

func (s *suite) agendaTimezone(ctx context.Context) error {
	cases := []struct {
		name     string
		token    string
		timezone string
		firstID  string
		firstAt  string
	}{
		{"Demo", s.reception, "America/Costa_Rica", appointmentID(demoPrefix, 8), monday + "T08:00:00-06:00"},
		{"Sur", s.southReception, "America/Bogota", appointmentID(southPrefix, 8), monday + "T08:00:00-05:00"},
	}

	for _, tc := range cases {
		agenda, err := s.getAgenda(ctx, tc.token, "date="+monday)
		if err != nil {
			return err
		}

		if agenda.Timezone != tc.timezone {
			return fmt.Errorf("%s: timezone = %q, want %q", tc.name, agenda.Timezone, tc.timezone)
		}

		found := false

		for _, item := range agenda.Items {
			if item.ID == tc.firstID {
				found = true

				if item.StartsAt != tc.firstAt {
					return fmt.Errorf("%s: starts_at = %q, want %q (hora de pared de la clínica)", tc.name, item.StartsAt, tc.firstAt)
				}
			}
		}

		if !found {
			return fmt.Errorf("%s: falta la cita %s", tc.name, tc.firstID)
		}
	}

	return nil
}

func (s *suite) agendaPermissions(ctx context.Context) error {
	path := "/api/v1/agenda?date=" + monday

	if _, err := s.expect(ctx, http.MethodGet, path, "", "", http.StatusUnauthorized); err != nil {
		return err
	}

	_, err := s.expect(ctx, http.MethodGet, path, s.platformAdmin, "", http.StatusForbidden)

	return err
}

func (s *suite) agendaDoesNotCrossClinics(ctx context.Context) error {
	north, err := s.getAgenda(ctx, s.northReception, "date="+monday)
	if err != nil {
		return err
	}

	if err := mustContain(north.ids(), appointmentID(northPrefix, 7), appointmentID(northPrefix, 8)); err != nil {
		return err
	}

	for _, id := range north.ids() {
		if !strings.HasPrefix(id, northPrefix) {
			return fmt.Errorf("la agenda de Norte trae la cita %s de otra clínica", id)
		}
	}

	// Un profesional de otra clínica no existe para quien consulta (404).
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/agenda?date="+monday+"&practitioner_id="+demoPractitioner1, s.northReception, "", http.StatusNotFound); err != nil {
		return err
	}

	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/agenda?date="+monday+"&practitioner_id="+northPractitioner1, s.reception, "", http.StatusNotFound); err != nil {
		return err
	}

	// Una clínica enviada en los parámetros se ignora: manda el token.
	forged, err := s.getAgenda(ctx, s.reception, "date="+monday+"&clinic_id="+northClinicID)
	if err != nil {
		return err
	}

	for _, id := range forged.ids() {
		if !strings.HasPrefix(id, demoPrefix) {
			return fmt.Errorf("un clinic_id falsificado cambió la agenda: trae la cita %s", id)
		}
	}

	return mustContain(forged.ids(), appointmentID(demoPrefix, 8))
}

// pastWeek es la semana del 9 al 14 de noviembre, que en la semilla solo tiene
// citas ya cerradas.
const pastWeek = "from=2026-11-09&to=2026-11-14"

func (s *suite) countsMatchTheSeed(ctx context.Context) error {
	cases := []struct {
		name     string
		token    string
		timezone string
		want     map[string]int
		total    int
	}{
		{"Demo", s.admin, "America/Costa_Rica",
			map[string]int{"booked": 0, "attended": 4, "no_show": 1, "cancelled": 1, "rescheduled": 1}, 7},
		{"Norte", s.northAdmin, "America/Costa_Rica",
			map[string]int{"booked": 0, "attended": 3, "no_show": 1, "cancelled": 1, "rescheduled": 1}, 6},
		{"Sur", s.southAdmin, "America/Bogota",
			map[string]int{"booked": 0, "attended": 3, "no_show": 1, "cancelled": 1, "rescheduled": 1}, 6},
	}

	for _, tc := range cases {
		got, err := s.getCounts(ctx, tc.token, pastWeek)
		if err != nil {
			return err
		}

		if got.Timezone != tc.timezone || got.Total != tc.total {
			return fmt.Errorf("%s: timezone = %q total = %d, want %q y %d", tc.name, got.Timezone, got.Total, tc.timezone, tc.total)
		}

		for status, want := range tc.want {
			if got.Counts[status] != want {
				return fmt.Errorf("%s: %s = %d, want %d (conteos: %v)", tc.name, status, got.Counts[status], want, got.Counts)
			}
		}
	}

	return nil
}

func (s *suite) countsRangeAndZeros(ctx context.Context) error {
	// Un solo día, con los dos extremos iguales: el sábado 14 de Norte tiene
	// una cita atendida.
	day, err := s.getCounts(ctx, s.northAdmin, "from=2026-11-14&to=2026-11-14")
	if err != nil {
		return err
	}

	if day.Total != 1 || day.Counts["attended"] != 1 {
		return fmt.Errorf("el 14 de noviembre en Norte: total = %d, conteos = %v, want una cita atendida", day.Total, day.Counts)
	}

	// Un rango sin citas devuelve los cinco estados, todos en cero.
	empty, err := s.getCounts(ctx, s.admin, "from=2020-01-01&to=2020-01-31")
	if err != nil {
		return err
	}

	if empty.Total != 0 || len(empty.Counts) != 5 {
		return fmt.Errorf("rango vacío: total = %d, conteos = %v, want cinco estados en cero", empty.Total, empty.Counts)
	}

	for _, status := range []string{"booked", "attended", "no_show", "cancelled", "rescheduled"} {
		if value, found := empty.Counts[status]; !found || value != 0 {
			return fmt.Errorf("rango vacío: %s = %d (presente: %t), want 0 presente", status, value, found)
		}
	}

	return nil
}

func (s *suite) countsInvalidInputs(ctx context.Context) error {
	invalid := []string{
		"",
		"from=2026-11-09",
		"to=2026-11-14",
		"from=09-11-2026&to=2026-11-14",
		"from=2026-11-09&to=14/11/2026",
		"from=2026-11-14&to=2026-11-09",
		"from=2026-01-01&to=2027-01-02",
	}

	for _, query := range invalid {
		if _, err := s.expect(ctx, http.MethodGet, "/api/v1/reports/appointments-by-status?"+query, s.admin, "", http.StatusBadRequest); err != nil {
			return err
		}
	}

	return nil
}

func (s *suite) countsPermissions(ctx context.Context) error {
	path := "/api/v1/reports/appointments-by-status?" + pastWeek

	if _, err := s.expect(ctx, http.MethodGet, path, "", "", http.StatusUnauthorized); err != nil {
		return err
	}

	// El conteo no está en la matriz del enunciado: se deja solo a la
	// administración de la clínica.
	for _, token := range []string{s.reception, s.practitioner, s.platformAdmin} {
		if _, err := s.expect(ctx, http.MethodGet, path, token, "", http.StatusForbidden); err != nil {
			return err
		}
	}

	return nil
}

func (s *suite) countsIgnoreForgedClinic(ctx context.Context) error {
	got, err := s.getCounts(ctx, s.admin, pastWeek+"&clinic_id="+northClinicID)
	if err != nil {
		return err
	}

	// Demo tiene 7 citas cerradas esa semana y Norte tiene 6.
	if got.Total != 7 {
		return fmt.Errorf("total = %d, want 7: un clinic_id falsificado cambió el conteo", got.Total)
	}

	return nil
}
