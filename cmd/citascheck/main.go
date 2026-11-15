// Command citascheck ejecuta las pruebas de integración de horarios,
// disponibilidad y citas contra la pila real de Compose: RF5 a RF13, la
// prueba de carrera, la caché de disponibilidad y los invariantes I1, I2, I3,
// I4, I5, I7 e I8. Cada prueba lleva en su nombre el requisito o invariante
// que sostiene, para la trazabilidad.
//
// Las pruebas no dependen de la semilla de horarios ni de citas: fijan el
// horario de dos profesionales de la clínica Demo, trabajan sobre fechas
// lejanas elegidas al azar y, al terminar, cancelan las citas que quedaron
// vigentes, borran sus bloqueos y restauran los horarios que había.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Identificadores fijos de la semilla (seed/agenda.sql).
const (
	demoClinic  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	northClinic = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	// practitionerA es el profesional del usuario "practitioner".
	practitionerA = "c1c1c1c1-0000-4000-8000-000000000001"
	serviceA      = "d1d1d1d1-0000-4000-8000-000000000001"
	// practitionerB es el profesional del usuario "practitioner2".
	practitionerB = "c1c1c1c1-0000-4000-8000-000000000002"
	serviceB      = "d1d1d1d1-0000-4000-8000-000000000003"

	northPractitioner = "c2c2c2c2-0000-4000-8000-000000000001"
)

const (
	// raceRounds y raceClients definen la prueba de carrera: en cada ronda,
	// raceClients peticiones simultáneas compiten por un mismo espacio.
	raceRounds  = 20
	raceClients = 12
)

type appointment struct {
	ID                 string  `json:"id"`
	ClinicID           string  `json:"clinic_id"`
	PatientID          string  `json:"patient_id"`
	PractitionerID     string  `json:"practitioner_id"`
	ServiceID          string  `json:"service_id"`
	StartsAt           string  `json:"starts_at"`
	EndsAt             string  `json:"ends_at"`
	Status             string  `json:"status"`
	RescheduledFromID  *string `json:"rescheduled_from_id"`
	ClosedBy           *string `json:"closed_by"`
	CancellationReason *string `json:"cancellation_reason"`
}

type slot struct {
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

type scheduleBlock struct {
	Weekday   int    `json:"weekday"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

type suite struct {
	apiURL      string
	keycloakURL string
	http        *http.Client
	expectCache bool

	admin, reception, practitioner, practitioner2 string
	northReception, northAdmin                    string

	// patients son pacientes creados para esta ejecución. Cada cliente de la
	// carrera usa uno distinto, para que el único conflicto posible sea el
	// del profesional (I1).
	patients []string
	// duration es la duración en minutos de serviceA.
	duration int
	// baseDay es una fecha lejana elegida al azar. Cada prueba usa un día
	// distinto a partir de ella, para no depender de las demás.
	baseDay time.Time

	savedSchedules map[string][]scheduleBlock
	// created son las citas que esta ejecución dejó vigentes y los bloqueos
	// que creó, para limpiarlos al final.
	mu      sync.Mutex
	created []string
	blocks  []string

	// attended es una cita atendida por practitionerA, para RF15.
	attended string
}

func main() {
	s := &suite{
		apiURL:         envOr("API_URL", "http://proxy:8080"),
		keycloakURL:    envOr("KEYCLOAK_URL", "http://keycloak:8080"),
		http:           &http.Client{Timeout: 15 * time.Second},
		expectCache:    envOr("EXPECT_CACHE", "true") != "false",
		savedSchedules: map[string][]scheduleBlock{},
	}

	random := rand.New(rand.NewSource(time.Now().UnixNano()))
	s.baseDay = time.Now().UTC().AddDate(0, 0, 60+random.Intn(1500))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := s.login(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}

	if err := s.setUp(ctx, random); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL: preparación:", err)
		s.tearDown(ctx)
		os.Exit(1)
	}

	failed := s.run(ctx)

	if err := s.tearDown(ctx); err != nil {
		fmt.Printf("FAIL: limpieza\n      %v\n", err)
		failed++
	}

	fmt.Println()

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %d prueba(s) de integración de citas fallaron\n", failed)
		os.Exit(1)
	}

	fmt.Println("All appointment integration checks passed.")
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return strings.TrimRight(value, "/")
}

// ---------------------------------------------------------------------------
// Apoyo
// ---------------------------------------------------------------------------

// login obtiene un token de cada usuario de prueba.
func (s *suite) login(ctx context.Context) error {
	users := []struct {
		target   *string
		username string
		password string
	}{
		{&s.admin, "clinic.admin", "Clinic123!"},
		{&s.reception, "reception", "Reception123!"},
		{&s.practitioner, "practitioner", "Practitioner123!"},
		{&s.practitioner2, "practitioner2", "Practitioner123!"},
		{&s.northReception, "norte.reception", "Norte123!"},
		{&s.northAdmin, "norte.admin", "Norte123!"},
	}

	for _, user := range users {
		token, err := s.token(ctx, user.username, user.password)
		if err != nil {
			return fmt.Errorf("token de %s: %w", user.username, err)
		}

		*user.target = token
	}

	return nil
}

// token pide un token a Keycloak. Reintenta porque Keycloak puede tardar unos
// segundos en aceptar peticiones.
func (s *suite) token(ctx context.Context, username, password string) (string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {"medicitas-cli"},
		"username":   {username},
		"password":   {password},
	}

	var lastErr error

	for attempt := 0; attempt < 20; attempt++ {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			s.keycloakURL+"/realms/medicitas/protocol/openid-connect/token",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			return "", err
		}

		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := s.http.Do(req)
		if err == nil {
			var body struct {
				AccessToken string `json:"access_token"`
			}

			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK && decodeErr == nil && body.AccessToken != "" {
				return body.AccessToken, nil
			}

			err = fmt.Errorf("Keycloak respondió %d", resp.StatusCode)
		}

		lastErr = err

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}

	return "", lastErr
}

// response es el resultado de una petición a la API.
type response struct {
	status int
	body   []byte
	header http.Header
}

func (r response) String() string {
	return fmt.Sprintf("%d %s", r.status, strings.TrimSpace(string(r.body)))
}

// decode lee el cuerpo como JSON en out.
func (r response) decode(out any) error {
	if err := json.Unmarshal(r.body, out); err != nil {
		return fmt.Errorf("respuesta no es el JSON esperado (%s): %w", r, err)
	}

	return nil
}

// call hace una petición a la API. body, si no es nil, se envía como JSON.
func (s *suite) call(ctx context.Context, method, path, token string, body any) (response, error) {
	var reader io.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return response{}, err
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.apiURL+path, reader)
	if err != nil {
		return response{}, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return response{}, err
	}

	return response{status: resp.StatusCode, body: raw, header: resp.Header}, nil
}

// expect hace una petición y falla si el código de estado no es el esperado.
func (s *suite) expect(ctx context.Context, want int, method, path, token string, body any) (response, error) {
	resp, err := s.call(ctx, method, path, token, body)
	if err != nil {
		return response{}, err
	}

	if resp.status != want {
		return resp, fmt.Errorf("%s %s: se esperaba %d y respondió %s", method, path, want, resp)
	}

	return resp, nil
}

// day devuelve la fecha AAAA-MM-DD del día número offset de esta ejecución.
func (s *suite) day(offset int) string {
	return s.baseDay.AddDate(0, 0, offset).Format("2006-01-02")
}

// slots consulta la disponibilidad de un profesional (RF7) y devuelve también
// el valor del encabezado X-Cache.
func (s *suite) slots(ctx context.Context, practitionerID, serviceID, date string) ([]slot, string, error) {
	path := fmt.Sprintf("/api/v1/practitioners/%s/availability?service_id=%s&date=%s", practitionerID, serviceID, date)

	resp, err := s.expect(ctx, http.StatusOK, http.MethodGet, path, s.reception, nil)
	if err != nil {
		return nil, "", err
	}

	var body struct {
		Slots []slot `json:"slots"`
	}
	if err := resp.decode(&body); err != nil {
		return nil, "", err
	}

	return body.Slots, resp.header.Get("X-Cache"), nil
}

// freeSlots devuelve los espacios de practitionerA con serviceA en un día y
// falla si hay menos de los que la prueba necesita.
func (s *suite) freeSlots(ctx context.Context, dayOffset, atLeast int) ([]slot, error) {
	slots, _, err := s.slots(ctx, practitionerA, serviceA, s.day(dayOffset))
	if err != nil {
		return nil, err
	}

	if len(slots) < atLeast {
		return nil, fmt.Errorf("el día %s tiene %d espacios y la prueba necesita %d", s.day(dayOffset), len(slots), atLeast)
	}

	return slots, nil
}

func hasSlot(slots []slot, startsAt string) bool {
	want, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return false
	}

	for _, current := range slots {
		got, err := time.Parse(time.RFC3339, current.StartsAt)
		if err == nil && got.Equal(want) {
			return true
		}
	}

	return false
}

// shift suma minutos a un instante RFC 3339 y conserva su zona horaria.
func shift(instant string, minutes int) string {
	parsed, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		return instant
	}

	return parsed.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
}

// bookBody es el cuerpo de una reserva.
func bookBody(patientID, practitionerID, serviceID, startsAt string) map[string]string {
	return map[string]string{
		"patient_id":      patientID,
		"practitioner_id": practitionerID,
		"service_id":      serviceID,
		"starts_at":       startsAt,
	}
}

// book reserva una cita con recepción y la anota para la limpieza final.
func (s *suite) book(ctx context.Context, patientID, practitionerID, serviceID, startsAt string) (appointment, error) {
	resp, err := s.expect(
		ctx, http.StatusCreated, http.MethodPost, "/api/v1/appointments", s.reception,
		bookBody(patientID, practitionerID, serviceID, startsAt),
	)
	if err != nil {
		return appointment{}, err
	}

	var created appointment
	if err := resp.decode(&created); err != nil {
		return appointment{}, err
	}

	s.track(created.ID)

	return created, nil
}

func (s *suite) track(appointmentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.created = append(s.created, appointmentID)
}

func (s *suite) getAppointment(ctx context.Context, id string) (appointment, error) {
	resp, err := s.expect(ctx, http.StatusOK, http.MethodGet, "/api/v1/appointments/"+id, s.reception, nil)
	if err != nil {
		return appointment{}, err
	}

	var result appointment

	return result, resp.decode(&result)
}

// putSchedule fija el horario de un profesional con la administración.
func (s *suite) putSchedule(ctx context.Context, practitionerID string, blocks []scheduleBlock) error {
	_, err := s.expect(
		ctx, http.StatusOK, http.MethodPut,
		"/api/v1/practitioners/"+practitionerID+"/schedule", s.admin,
		map[string]any{"blocks": blocks},
	)

	return err
}

// splitDays es una jornada partida todos los días: mañana y tarde.
func splitDays() []scheduleBlock {
	blocks := make([]scheduleBlock, 0, 14)

	for weekday := 1; weekday <= 7; weekday++ {
		blocks = append(blocks,
			scheduleBlock{Weekday: weekday, StartTime: "08:00", EndTime: "12:00"},
			scheduleBlock{Weekday: weekday, StartTime: "13:00", EndTime: "17:00"},
		)
	}

	return blocks
}

// allDay es un horario que cubre las 24 horas de todos los días. Lo usa la
// prueba de RF12, que necesita una cita que empiece "ahora".
func allDay() []scheduleBlock {
	blocks := make([]scheduleBlock, 0, 7)

	for weekday := 1; weekday <= 7; weekday++ {
		blocks = append(blocks, scheduleBlock{Weekday: weekday, StartTime: "00:00", EndTime: "24:00"})
	}

	return blocks
}

// setUp guarda los horarios actuales, fija los de prueba y crea los pacientes.
func (s *suite) setUp(ctx context.Context, random *rand.Rand) error {
	for _, practitionerID := range []string{practitionerA, practitionerB} {
		resp, err := s.expect(
			ctx, http.StatusOK, http.MethodGet,
			"/api/v1/practitioners/"+practitionerID+"/schedule", s.admin, nil,
		)
		if err != nil {
			return err
		}

		var body struct {
			Blocks []scheduleBlock `json:"blocks"`
		}
		if err := resp.decode(&body); err != nil {
			return err
		}

		s.savedSchedules[practitionerID] = body.Blocks

		if err := s.putSchedule(ctx, practitionerID, splitDays()); err != nil {
			return err
		}
	}

	resp, err := s.expect(ctx, http.StatusOK, http.MethodGet, "/api/v1/services/"+serviceA, s.admin, nil)
	if err != nil {
		return err
	}

	var service struct {
		DurationMinutes int `json:"duration_minutes"`
	}
	if err := resp.decode(&service); err != nil {
		return err
	}

	s.duration = service.DurationMinutes

	runID := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 10)

	for i := 0; i < raceClients; i++ {
		resp, err := s.expect(ctx, http.StatusCreated, http.MethodPost, "/api/v1/patients", s.reception, map[string]string{
			"identification": fmt.Sprintf("CITAS-%s-%d-%d", runID, i, random.Intn(1000)),
			"first_name":     "Paciente",
			"last_name":      fmt.Sprintf("De Prueba %d", i),
			"birth_date":     "1990-05-17",
		})
		if err != nil {
			return err
		}

		var patient struct {
			ID string `json:"id"`
		}
		if err := resp.decode(&patient); err != nil {
			return err
		}

		s.patients = append(s.patients, patient.ID)
	}

	return nil
}

// tearDown deja la base como estaba en lo que se puede: cancela las citas que
// siguen vigentes, borra los bloqueos y restaura los horarios. Las citas en
// un estado final se quedan, porque las citas no se borran.
func (s *suite) tearDown(ctx context.Context) error {
	var firstErr error

	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, id := range s.created {
		resp, err := s.call(ctx, http.MethodPost, "/api/v1/appointments/"+id+"/cancel", s.reception, map[string]string{
			"reason": "Limpieza de la prueba de integración",
		})
		if err != nil {
			keep(err)
			continue
		}

		// 409 significa que la cita ya estaba en un estado final.
		if resp.status != http.StatusOK && resp.status != http.StatusConflict {
			keep(fmt.Errorf("cancelar %s: %s", id, resp))
		}
	}

	for _, path := range s.blocks {
		resp, err := s.call(ctx, http.MethodDelete, path, s.admin, nil)
		if err != nil {
			keep(err)
			continue
		}

		if resp.status != http.StatusNoContent && resp.status != http.StatusNotFound {
			keep(fmt.Errorf("borrar %s: %s", path, resp))
		}
	}

	for practitionerID, blocks := range s.savedSchedules {
		if blocks == nil {
			blocks = []scheduleBlock{}
		}

		keep(s.putSchedule(ctx, practitionerID, blocks))
	}

	return firstErr
}

// run ejecuta todas las pruebas en orden y devuelve cuántas fallaron.
func (s *suite) run(ctx context.Context) int {
	tests := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"RF5 el horario admite varios bloques por día y se puede consultar", s.scheduleRoundTrip},
		{"RF5 los horarios inválidos se rechazan (400)", s.invalidSchedules},
		{"RF5 permisos: recepción no (403), el profesional solo el propio (200 y 403), sin token (401)", s.schedulePermissions},
		{"RF7 la disponibilidad sale del horario y de la duración del servicio", s.availabilityFollowsSchedule},
		{"RF7 las consultas inválidas se rechazan (400) y lo inexistente no se encuentra (404)", s.invalidAvailability},
		{"RF9 reservar concede la cita (201)", s.bookGranted},
		{"I4 la duración la fija el servicio, no la petición", s.durationComesFromService},
		{"I8 la disponibilidad no ofrece un espacio recién reservado, aunque estuviera en caché", s.cacheNeverLies},
		{"I8 un cambio en los servicios del profesional también invalida la caché", s.catalogChangeInvalidatesCache},
		{"RF9 I1 un espacio tomado o solapado responde 409", s.takenSlotIsConflict},
		{"I2 el mismo paciente no puede tener dos citas solapadas con otro profesional (409)", s.patientOverlapIsConflict},
		{"RF9 las solicitudes inválidas responden 400 y lo inexistente 404", s.invalidBookings},
		{"RF9 permisos: el profesional no reserva (403), sin token (401)", s.bookingPermissions},
		{"I3 no se reserva fuera del horario de atención (409)", s.outsideScheduleIsConflict},
		{"RF6 I3 un bloqueo retira la disponibilidad y rechaza reservas (409)", s.blockRemovesAvailability},
		{"RF6 un bloqueo no cancela las citas reservadas: las lista como conflictos", s.blockListsConflicts},
		{"RF6 permisos y entradas inválidas de los bloqueos", s.blockPermissionsAndValidation},
		{"RF5 cambiar el horario lista las citas que quedan fuera", s.scheduleChangeListsConflicts},
		{"RF10 I7 reprogramar libera el espacio anterior y ocupa el nuevo", s.rescheduleMovesTheAppointment},
		{"RF10 si el espacio nuevo no está libre, la cita original queda intacta (409)", s.failedRescheduleKeepsOriginal},
		{"RF10 reprogramar con un profesional que no atiende el servicio se rechaza (409)", s.rescheduleToOtherPractitioner},
		{"RF11 cancelar exige motivo (400) y registra quién canceló", s.cancelRecordsReasonAndUser},
		{"RF11 I7 el espacio cancelado vuelve a estar disponible de inmediato", s.cancelFreesTheSlot},
		{"RF13 un estado final no admite ninguna transición (409)", s.finalStatesAreFinal},
		{"RF12 el desenlace no se registra antes de la hora de inicio (409)", s.outcomeNotBeforeStart},
		{"RF12 RF13 desenlace de una cita ya iniciada, una sola vez", s.outcomeAfterStart},
		{"RF8 disponibilidad por servicio en un rango de fechas", s.availabilityByService},
		{"I1 carrera: muchos clientes por el último espacio, una sola reserva concedida", s.raceForTheLastSlot},
		{"I2 carrera: el mismo paciente con dos profesionales a la vez, una sola reserva", s.raceForTheSamePatient},
		{"I5 otra clínica no ve ni modifica citas, horarios, bloqueos ni disponibilidad (404)", s.otherClinicIsIsolated},
		{"I5 un clinic_id falsificado en el cuerpo o en la consulta se ignora", s.forgedClinicIsIgnored},
		{"RF15 la ruta interna de contexto de cita no se expone por el proxy (404)", s.internalRouteIsHidden},
		{"RF15 una nota clínica sobre una cita atendida se acepta de punta a punta (201)", s.clinicalNoteEndToEnd},
	}

	failed := 0

	for _, test := range tests {
		if err := test.fn(ctx); err != nil {
			failed++

			fmt.Printf("FAIL: %s\n      %v\n", test.name, err)

			continue
		}

		fmt.Printf("PASS: %s\n", test.name)
	}

	return failed
}

// ---------------------------------------------------------------------------
// Horarios (RF5)
// ---------------------------------------------------------------------------

func (s *suite) scheduleRoundTrip(ctx context.Context) error {
	resp, err := s.expect(ctx, http.StatusOK, http.MethodGet, "/api/v1/practitioners/"+practitionerA+"/schedule", s.reception, nil)
	if err != nil {
		return err
	}

	var body struct {
		Blocks []scheduleBlock `json:"blocks"`
	}
	if err := resp.decode(&body); err != nil {
		return err
	}

	if len(body.Blocks) != 14 {
		return fmt.Errorf("el horario tiene %d bloques y se esperaban 14 (dos por día)", len(body.Blocks))
	}

	first := body.Blocks[0]
	if first.Weekday != 1 || first.StartTime != "08:00" || first.EndTime != "12:00" {
		return fmt.Errorf("primer bloque inesperado: %+v", first)
	}

	return nil
}

func (s *suite) invalidSchedules(ctx context.Context) error {
	invalid := [][]scheduleBlock{
		{{Weekday: 1, StartTime: "08:00", EndTime: "12:00"}, {Weekday: 1, StartTime: "11:00", EndTime: "13:00"}},
		{{Weekday: 8, StartTime: "08:00", EndTime: "12:00"}},
		{{Weekday: 1, StartTime: "12:00", EndTime: "08:00"}},
		{{Weekday: 1, StartTime: "8am", EndTime: "12:00"}},
		{{Weekday: 1, StartTime: "08:00", EndTime: "25:00"}},
	}

	path := "/api/v1/practitioners/" + practitionerA + "/schedule"

	for _, blocks := range invalid {
		if _, err := s.expect(ctx, http.StatusBadRequest, http.MethodPut, path, s.admin, map[string]any{"blocks": blocks}); err != nil {
			return err
		}
	}

	// Un horario rechazado no debe haber tocado el vigente.
	return s.scheduleRoundTrip(ctx)
}

func (s *suite) schedulePermissions(ctx context.Context) error {
	body := map[string]any{"blocks": splitDays()}
	own := "/api/v1/practitioners/" + practitionerA + "/schedule"
	other := "/api/v1/practitioners/" + practitionerB + "/schedule"

	checks := []struct {
		want  int
		path  string
		token string
	}{
		{http.StatusForbidden, own, s.reception},
		{http.StatusOK, own, s.practitioner},
		{http.StatusForbidden, other, s.practitioner},
		{http.StatusUnauthorized, own, ""},
	}

	for _, check := range checks {
		if _, err := s.expect(ctx, check.want, http.MethodPut, check.path, check.token, body); err != nil {
			return err
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Disponibilidad (RF7, RF8) y caché (I8)
// ---------------------------------------------------------------------------

func (s *suite) availabilityFollowsSchedule(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 0, 1)
	if err != nil {
		return err
	}

	// Dos bloques de cuatro horas, sin citas: caben 240/duración en cada uno.
	want := 2 * (240 / s.duration)
	if len(slots) != want {
		return fmt.Errorf("hay %d espacios y se esperaban %d para un servicio de %d minutos", len(slots), want, s.duration)
	}

	for _, current := range slots {
		start, errStart := time.Parse(time.RFC3339, current.StartsAt)
		end, errEnd := time.Parse(time.RFC3339, current.EndsAt)
		if errStart != nil || errEnd != nil {
			return fmt.Errorf("espacio con fechas inválidas: %+v", current)
		}

		if end.Sub(start) != time.Duration(s.duration)*time.Minute {
			return fmt.Errorf("el espacio %+v no dura %d minutos", current, s.duration)
		}

		// Las horas vienen en la zona de la clínica: ninguna cae en la pausa.
		if hour := start.Hour(); hour < 8 || hour == 12 || hour >= 17 {
			return fmt.Errorf("el espacio %s está fuera del horario", current.StartsAt)
		}
	}

	if first, _ := time.Parse(time.RFC3339, slots[0].StartsAt); first.Hour() != 8 || first.Minute() != 0 {
		return fmt.Errorf("el primer espacio empieza a las %s y se esperaban las 08:00 de la clínica", slots[0].StartsAt)
	}

	return nil
}

func (s *suite) invalidAvailability(ctx context.Context) error {
	base := "/api/v1/practitioners/" + practitionerA + "/availability"

	checks := []struct {
		want int
		path string
	}{
		{http.StatusBadRequest, base + "?service_id=" + serviceA},
		{http.StatusBadRequest, base + "?service_id=" + serviceA + "&date=17-05-2030"},
		{http.StatusBadRequest, base + "?service_id=abc&date=" + s.day(0)},
		{http.StatusNotFound, base + "?service_id=d1d1d1d1-0000-4000-8000-00000000ffff&date=" + s.day(0)},
		{http.StatusNotFound, "/api/v1/practitioners/c1c1c1c1-0000-4000-8000-00000000ffff/availability?service_id=" + serviceA + "&date=" + s.day(0)},
	}

	for _, check := range checks {
		if _, err := s.expect(ctx, check.want, http.MethodGet, check.path, s.reception, nil); err != nil {
			return err
		}
	}

	// Un servicio que el profesional no atiende no tiene espacios.
	slots, _, err := s.slots(ctx, practitionerA, serviceB, s.day(0))
	if err != nil {
		return err
	}

	if len(slots) != 0 {
		return fmt.Errorf("hay %d espacios para un servicio que el profesional no atiende", len(slots))
	}

	return nil
}

func (s *suite) bookGranted(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 1, 2)
	if err != nil {
		return err
	}

	created, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	if created.Status != "booked" || created.ClinicID != demoClinic || created.PatientID != s.patients[0] {
		return fmt.Errorf("cita inesperada: %+v", created)
	}

	stored, err := s.getAppointment(ctx, created.ID)
	if err != nil {
		return err
	}

	if stored.ID != created.ID || stored.Status != "booked" {
		return fmt.Errorf("la cita leída no coincide con la creada: %+v", stored)
	}

	return nil
}

func (s *suite) durationComesFromService(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 2, 1)
	if err != nil {
		return err
	}

	// La petición intenta fijar una cita de un día entero.
	body := map[string]string{
		"patient_id":       s.patients[0],
		"practitioner_id":  practitionerA,
		"service_id":       serviceA,
		"starts_at":        slots[0].StartsAt,
		"ends_at":          shift(slots[0].StartsAt, 24*60),
		"duration_minutes": "600",
	}

	resp, err := s.expect(ctx, http.StatusCreated, http.MethodPost, "/api/v1/appointments", s.reception, body)
	if err != nil {
		return err
	}

	var created appointment
	if err := resp.decode(&created); err != nil {
		return err
	}

	s.track(created.ID)

	start, _ := time.Parse(time.RFC3339, created.StartsAt)
	end, _ := time.Parse(time.RFC3339, created.EndsAt)

	if end.Sub(start) != time.Duration(s.duration)*time.Minute {
		return fmt.Errorf("la cita dura %s y el servicio dura %d minutos", end.Sub(start), s.duration)
	}

	return nil
}

// cacheNeverLies es el criterio de aceptación 5: consultar, reservar uno de
// los espacios devueltos y volver a consultar. La segunda respuesta no puede
// traer el espacio, sin esperar a que venza nada.
func (s *suite) cacheNeverLies(ctx context.Context) error {
	date := s.day(3)

	before, _, err := s.slots(ctx, practitionerA, serviceA, date)
	if err != nil {
		return err
	}

	if len(before) < 2 {
		return fmt.Errorf("el día %s no tiene espacios suficientes", date)
	}

	// La segunda consulta idéntica debe salir de la caché.
	_, cache, err := s.slots(ctx, practitionerA, serviceA, date)
	if err != nil {
		return err
	}

	if s.expectCache && cache != "hit" {
		return fmt.Errorf("la segunda consulta respondió X-Cache=%q y se esperaba hit", cache)
	}

	target := before[1].StartsAt

	if _, err := s.book(ctx, s.patients[0], practitionerA, serviceA, target); err != nil {
		return err
	}

	after, cache, err := s.slots(ctx, practitionerA, serviceA, date)
	if err != nil {
		return err
	}

	if hasSlot(after, target) {
		return fmt.Errorf("la disponibilidad sigue ofreciendo %s después de reservarlo (X-Cache=%q)", target, cache)
	}

	if len(after) != len(before)-1 {
		return fmt.Errorf("había %d espacios, se reservó uno y ahora hay %d", len(before), len(after))
	}

	if s.expectCache && cache != "miss" {
		return fmt.Errorf("la consulta posterior a la reserva respondió X-Cache=%q: sirvió una entrada anterior al cambio", cache)
	}

	return nil
}

// catalogChangeInvalidatesCache cubre los cambios que afectan a la
// disponibilidad sin pasar por las citas: aquí, los servicios que atiende el
// profesional.
func (s *suite) catalogChangeInvalidatesCache(ctx context.Context) error {
	date := s.day(3)

	// Dos consultas: la segunda deja claro que la agenda está en caché.
	for i := 0; i < 2; i++ {
		if _, _, err := s.slots(ctx, practitionerA, serviceA, date); err != nil {
			return err
		}
	}

	resp, err := s.expect(ctx, http.StatusOK, http.MethodGet, "/api/v1/practitioners/"+practitionerA, s.admin, nil)
	if err != nil {
		return err
	}

	var practitioner struct {
		ServiceIDs []string `json:"service_ids"`
	}
	if err := resp.decode(&practitioner); err != nil {
		return err
	}

	// Se vuelven a guardar los mismos servicios: no cambia nada, pero es la
	// operación que debe invalidar.
	if _, err := s.expect(
		ctx, http.StatusOK, http.MethodPut, "/api/v1/practitioners/"+practitionerA+"/services", s.admin,
		map[string]any{"service_ids": practitioner.ServiceIDs},
	); err != nil {
		return err
	}

	_, cache, err := s.slots(ctx, practitionerA, serviceA, date)
	if err != nil {
		return err
	}

	if s.expectCache && cache != "miss" {
		return fmt.Errorf("después de cambiar los servicios la consulta respondió X-Cache=%q", cache)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Reserva (RF9) e invariantes I1, I2, I3
// ---------------------------------------------------------------------------

func (s *suite) takenSlotIsConflict(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 4, 4)
	if err != nil {
		return err
	}

	// Se reserva el segundo espacio para poder probar un solape por delante
	// y otro por detrás sin salirse del horario.
	taken := slots[1].StartsAt

	if _, err := s.book(ctx, s.patients[0], practitionerA, serviceA, taken); err != nil {
		return err
	}

	for _, startsAt := range []string{taken, shift(taken, 5), shift(taken, -5)} {
		resp, err := s.expect(
			ctx, http.StatusConflict, http.MethodPost, "/api/v1/appointments", s.reception,
			bookBody(s.patients[1], practitionerA, serviceA, startsAt),
		)
		if err != nil {
			return err
		}

		if !strings.Contains(string(resp.body), "slot already taken") {
			return fmt.Errorf("el 409 no dice que el espacio ya está tomado: %s", resp)
		}
	}

	// Una cita contigua sí cabe: termina justo donde empieza la otra.
	_, err = s.book(ctx, s.patients[1], practitionerA, serviceA, slots[2].StartsAt)

	return err
}

func (s *suite) patientOverlapIsConflict(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 5, 1)
	if err != nil {
		return err
	}

	if _, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt); err != nil {
		return err
	}

	// Mismo paciente, misma hora, otro profesional.
	resp, err := s.expect(
		ctx, http.StatusConflict, http.MethodPost, "/api/v1/appointments", s.reception,
		bookBody(s.patients[0], practitionerB, serviceB, shift(slots[0].StartsAt, 5)),
	)
	if err != nil {
		return err
	}

	if !strings.Contains(string(resp.body), "patient already has an appointment") {
		return fmt.Errorf("el 409 no dice que el paciente ya tiene una cita: %s", resp)
	}

	// Otro paciente sí puede tomar ese espacio del segundo profesional.
	_, err = s.book(ctx, s.patients[1], practitionerB, serviceB, shift(slots[0].StartsAt, 5))

	return err
}

func (s *suite) invalidBookings(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 6, 1)
	if err != nil {
		return err
	}

	startsAt := slots[0].StartsAt
	past := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	missing := "c1c1c1c1-0000-4000-8000-00000000ffff"

	checks := []struct {
		want int
		body any
	}{
		{http.StatusBadRequest, bookBody("abc", practitionerA, serviceA, startsAt)},
		{http.StatusBadRequest, bookBody(s.patients[0], practitionerA, serviceA, "mañana a las nueve")},
		{http.StatusBadRequest, bookBody(s.patients[0], practitionerA, serviceA, "2030-03-04 09:00")},
		{http.StatusBadRequest, bookBody(s.patients[0], practitionerA, serviceA, past)},
		{http.StatusBadRequest, map[string]string{}},
		{http.StatusBadRequest, "esto no es un objeto"},
		{http.StatusNotFound, bookBody(missing, practitionerA, serviceA, startsAt)},
		{http.StatusNotFound, bookBody(s.patients[0], missing, serviceA, startsAt)},
		{http.StatusNotFound, bookBody(s.patients[0], practitionerA, missing, startsAt)},
		// El servicio existe, pero este profesional no lo atiende.
		{http.StatusConflict, bookBody(s.patients[0], practitionerA, serviceB, startsAt)},
	}

	for _, check := range checks {
		if _, err := s.expect(ctx, check.want, http.MethodPost, "/api/v1/appointments", s.reception, check.body); err != nil {
			return err
		}
	}

	// Después de tantas entradas malas el servicio sigue respondiendo.
	_, err = s.freeSlots(ctx, 6, 1)

	return err
}

func (s *suite) bookingPermissions(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 6, 1)
	if err != nil {
		return err
	}

	body := bookBody(s.patients[0], practitionerA, serviceA, slots[0].StartsAt)

	if _, err := s.expect(ctx, http.StatusForbidden, http.MethodPost, "/api/v1/appointments", s.practitioner, body); err != nil {
		return err
	}

	_, err = s.expect(ctx, http.StatusUnauthorized, http.MethodPost, "/api/v1/appointments", "", body)

	return err
}

func (s *suite) outsideScheduleIsConflict(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 7, 1)
	if err != nil {
		return err
	}

	morning := slots[0].StartsAt // 08:00 de la clínica

	outside := []string{
		shift(morning, -60),               // 07:00, antes de abrir
		shift(morning, 4*60+15),           // 12:15, en la pausa
		shift(morning, 4*60-s.duration/2), // empieza dentro, termina en la pausa
		shift(morning, 9*60),              // 17:00, después de cerrar
	}

	for _, startsAt := range outside {
		resp, err := s.expect(
			ctx, http.StatusConflict, http.MethodPost, "/api/v1/appointments", s.reception,
			bookBody(s.patients[0], practitionerA, serviceA, startsAt),
		)
		if err != nil {
			return err
		}

		if !strings.Contains(string(resp.body), "outside the practitioner's schedule") {
			return fmt.Errorf("el 409 no dice que está fuera del horario: %s", resp)
		}
	}

	return nil
}

// createBlock registra un bloqueo y lo anota para la limpieza final.
func (s *suite) createBlock(ctx context.Context, startsAt, endsAt string) (string, []appointment, error) {
	resp, err := s.expect(
		ctx, http.StatusCreated, http.MethodPost, "/api/v1/practitioners/"+practitionerA+"/blocks", s.admin,
		map[string]string{"starts_at": startsAt, "ends_at": endsAt, "reason": "Permiso de la prueba de integración"},
	)
	if err != nil {
		return "", nil, err
	}

	var body struct {
		Block struct {
			ID string `json:"id"`
		} `json:"block"`
		Conflicts []appointment `json:"conflicts"`
	}
	if err := resp.decode(&body); err != nil {
		return "", nil, err
	}

	path := "/api/v1/practitioners/" + practitionerA + "/blocks/" + body.Block.ID

	s.mu.Lock()
	s.blocks = append(s.blocks, path)
	s.mu.Unlock()

	return path, body.Conflicts, nil
}

func (s *suite) blockRemovesAvailability(ctx context.Context) error {
	before, err := s.freeSlots(ctx, 8, 4)
	if err != nil {
		return err
	}

	// Se bloquean los dos primeros espacios del día.
	blockStart, blockEnd := before[0].StartsAt, before[1].EndsAt

	path, conflicts, err := s.createBlock(ctx, blockStart, blockEnd)
	if err != nil {
		return err
	}

	if len(conflicts) != 0 {
		return fmt.Errorf("un bloqueo sobre espacios libres reportó %d conflictos", len(conflicts))
	}

	after, _, err := s.slots(ctx, practitionerA, serviceA, s.day(8))
	if err != nil {
		return err
	}

	if hasSlot(after, before[0].StartsAt) || hasSlot(after, before[1].StartsAt) {
		return fmt.Errorf("la disponibilidad sigue ofreciendo espacios dentro del bloqueo")
	}

	if len(after) != len(before)-2 {
		return fmt.Errorf("había %d espacios, se bloquearon 2 y ahora hay %d", len(before), len(after))
	}

	resp, err := s.expect(
		ctx, http.StatusConflict, http.MethodPost, "/api/v1/appointments", s.reception,
		bookBody(s.patients[0], practitionerA, serviceA, before[0].StartsAt),
	)
	if err != nil {
		return err
	}

	if !strings.Contains(string(resp.body), "blocked") {
		return fmt.Errorf("el 409 no dice que la agenda está bloqueada: %s", resp)
	}

	// El bloqueo aparece en el listado y, al borrarlo, vuelve el espacio.
	resp, err = s.expect(
		ctx, http.StatusOK, http.MethodGet,
		"/api/v1/practitioners/"+practitionerA+"/blocks?from="+s.day(8)+"&to="+s.day(8), s.reception, nil,
	)
	if err != nil {
		return err
	}

	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := resp.decode(&listed); err != nil {
		return err
	}

	if len(listed.Items) != 1 || !strings.HasSuffix(path, listed.Items[0].ID) {
		return fmt.Errorf("el listado de bloqueos del día no trae solo el bloqueo creado: %s", resp)
	}

	if _, err := s.expect(ctx, http.StatusNoContent, http.MethodDelete, path, s.admin, nil); err != nil {
		return err
	}

	restored, _, err := s.slots(ctx, practitionerA, serviceA, s.day(8))
	if err != nil {
		return err
	}

	if len(restored) != len(before) {
		return fmt.Errorf("al borrar el bloqueo hay %d espacios y antes había %d", len(restored), len(before))
	}

	return nil
}

func (s *suite) blockListsConflicts(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 9, 3)
	if err != nil {
		return err
	}

	inside, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	outside, err := s.book(ctx, s.patients[1], practitionerA, serviceA, slots[2].StartsAt)
	if err != nil {
		return err
	}

	path, conflicts, err := s.createBlock(ctx, slots[0].StartsAt, slots[1].EndsAt)
	if err != nil {
		return err
	}

	if len(conflicts) != 1 || conflicts[0].ID != inside.ID {
		return fmt.Errorf("el bloqueo debía reportar solo la cita %s como conflicto y reportó %+v", inside.ID, conflicts)
	}

	// La cita sigue vigente: el bloqueo no cancela.
	for _, id := range []string{inside.ID, outside.ID} {
		stored, err := s.getAppointment(ctx, id)
		if err != nil {
			return err
		}

		if stored.Status != "booked" {
			return fmt.Errorf("la cita %s quedó en %s después del bloqueo", id, stored.Status)
		}
	}

	resp, err := s.expect(ctx, http.StatusOK, http.MethodGet, path+"/conflicts", s.reception, nil)
	if err != nil {
		return err
	}

	var listed struct {
		Items []appointment `json:"items"`
	}
	if err := resp.decode(&listed); err != nil {
		return err
	}

	if len(listed.Items) != 1 || listed.Items[0].ID != inside.ID {
		return fmt.Errorf("la consulta de conflictos no trae solo la cita %s: %s", inside.ID, resp)
	}

	// Recepción la reprograma fuera del bloqueo y el conflicto desaparece.
	if _, err := s.reschedule(ctx, http.StatusCreated, inside.ID, slots[len(slots)-1].StartsAt, ""); err != nil {
		return err
	}

	resp, err = s.expect(ctx, http.StatusOK, http.MethodGet, path+"/conflicts", s.reception, nil)
	if err != nil {
		return err
	}

	if err := resp.decode(&listed); err != nil {
		return err
	}

	if len(listed.Items) != 0 {
		return fmt.Errorf("después de reprogramar siguen quedando conflictos: %s", resp)
	}

	return nil
}

func (s *suite) blockPermissionsAndValidation(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 10, 2)
	if err != nil {
		return err
	}

	valid := map[string]string{"starts_at": slots[0].StartsAt, "ends_at": slots[0].EndsAt, "reason": "Vacaciones"}
	own := "/api/v1/practitioners/" + practitionerA + "/blocks"
	other := "/api/v1/practitioners/" + practitionerB + "/blocks"

	checks := []struct {
		want  int
		path  string
		token string
		body  any
	}{
		{http.StatusForbidden, own, s.reception, valid},
		{http.StatusForbidden, other, s.practitioner, valid},
		{http.StatusUnauthorized, own, "", valid},
		{http.StatusBadRequest, own, s.admin, map[string]string{"starts_at": slots[0].EndsAt, "ends_at": slots[0].StartsAt, "reason": "Al revés"}},
		{http.StatusBadRequest, own, s.admin, map[string]string{"starts_at": slots[0].StartsAt, "ends_at": slots[0].EndsAt, "reason": "  "}},
		{http.StatusBadRequest, own, s.admin, map[string]string{"starts_at": "ayer", "ends_at": slots[0].EndsAt, "reason": "Fecha mala"}},
	}

	for _, check := range checks {
		if _, err := s.expect(ctx, check.want, http.MethodPost, check.path, check.token, check.body); err != nil {
			return err
		}
	}

	// El profesional sí administra sus propios bloqueos.
	resp, err := s.expect(ctx, http.StatusCreated, http.MethodPost, own, s.practitioner, valid)
	if err != nil {
		return err
	}

	var body struct {
		Block struct {
			ID string `json:"id"`
		} `json:"block"`
	}
	if err := resp.decode(&body); err != nil {
		return err
	}

	_, err = s.expect(ctx, http.StatusNoContent, http.MethodDelete, own+"/"+body.Block.ID, s.practitioner, nil)

	return err
}

func (s *suite) scheduleChangeListsConflicts(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 11, 1)
	if err != nil {
		return err
	}

	// Una cita en la tarde; después el horario se reduce a la mañana.
	afternoon := slots[len(slots)-1].StartsAt

	created, err := s.book(ctx, s.patients[0], practitionerA, serviceA, afternoon)
	if err != nil {
		return err
	}

	mornings := make([]scheduleBlock, 0, 7)
	for weekday := 1; weekday <= 7; weekday++ {
		mornings = append(mornings, scheduleBlock{Weekday: weekday, StartTime: "08:00", EndTime: "12:00"})
	}

	resp, err := s.expect(
		ctx, http.StatusOK, http.MethodPut, "/api/v1/practitioners/"+practitionerA+"/schedule", s.admin,
		map[string]any{"blocks": mornings},
	)
	if err != nil {
		return err
	}

	var body struct {
		Conflicts []appointment `json:"conflicts"`
	}
	if err := resp.decode(&body); err != nil {
		return err
	}

	found := false
	for _, conflict := range body.Conflicts {
		if conflict.ID == created.ID {
			found = true
		}
	}

	// El horario de prueba se restaura antes de evaluar, para no afectar a
	// las pruebas siguientes si esta falla.
	if err := s.putSchedule(ctx, practitionerA, splitDays()); err != nil {
		return err
	}

	if !found {
		return fmt.Errorf("la cita %s quedó fuera del horario nuevo y no aparece en los conflictos", created.ID)
	}

	stored, err := s.getAppointment(ctx, created.ID)
	if err != nil {
		return err
	}

	if stored.Status != "booked" {
		return fmt.Errorf("cambiar el horario dejó la cita en %s", stored.Status)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Reprogramar, cancelar y desenlace (RF10 a RF13)
// ---------------------------------------------------------------------------

// reschedule reprograma una cita y devuelve la nueva.
func (s *suite) reschedule(ctx context.Context, want int, id, startsAt, practitionerID string) (appointment, error) {
	body := map[string]string{"starts_at": startsAt}
	if practitionerID != "" {
		body["practitioner_id"] = practitionerID
	}

	resp, err := s.expect(ctx, want, http.MethodPost, "/api/v1/appointments/"+id+"/reschedule", s.reception, body)
	if err != nil || want != http.StatusCreated {
		return appointment{}, err
	}

	var created appointment
	if err := resp.decode(&created); err != nil {
		return appointment{}, err
	}

	s.track(created.ID)

	return created, nil
}

func (s *suite) rescheduleMovesTheAppointment(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 12, 3)
	if err != nil {
		return err
	}

	original, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	moved, err := s.reschedule(ctx, http.StatusCreated, original.ID, slots[2].StartsAt, "")
	if err != nil {
		return err
	}

	if moved.RescheduledFromID == nil || *moved.RescheduledFromID != original.ID {
		return fmt.Errorf("la cita nueva no apunta a la anterior: %+v", moved)
	}

	if moved.Status != "booked" || moved.PatientID != original.PatientID || moved.ServiceID != original.ServiceID {
		return fmt.Errorf("la cita nueva no conserva los datos de la anterior: %+v", moved)
	}

	old, err := s.getAppointment(ctx, original.ID)
	if err != nil {
		return err
	}

	if old.Status != "rescheduled" {
		return fmt.Errorf("la cita anterior quedó en %s y se esperaba rescheduled", old.Status)
	}

	after, _, err := s.slots(ctx, practitionerA, serviceA, s.day(12))
	if err != nil {
		return err
	}

	if !hasSlot(after, slots[0].StartsAt) {
		return fmt.Errorf("el espacio anterior %s no quedó libre", slots[0].StartsAt)
	}

	if hasSlot(after, slots[2].StartsAt) {
		return fmt.Errorf("el espacio nuevo %s sigue apareciendo libre", slots[2].StartsAt)
	}

	// La cita también puede moverse a un espacio que se solapa con el que
	// tiene: unos minutos más tarde.
	_, err = s.reschedule(ctx, http.StatusCreated, moved.ID, shift(slots[2].StartsAt, 5), "")

	return err
}

func (s *suite) failedRescheduleKeepsOriginal(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 13, 2)
	if err != nil {
		return err
	}

	original, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	if _, err := s.book(ctx, s.patients[1], practitionerA, serviceA, slots[1].StartsAt); err != nil {
		return err
	}

	// Hacia un espacio tomado y hacia fuera del horario.
	for _, startsAt := range []string{slots[1].StartsAt, shift(slots[0].StartsAt, -120)} {
		if _, err := s.reschedule(ctx, http.StatusConflict, original.ID, startsAt, ""); err != nil {
			return err
		}
	}

	stored, err := s.getAppointment(ctx, original.ID)
	if err != nil {
		return err
	}

	if stored.Status != "booked" {
		return fmt.Errorf("una reprogramación fallida dejó la cita original en %s", stored.Status)
	}

	after, _, err := s.slots(ctx, practitionerA, serviceA, s.day(13))
	if err != nil {
		return err
	}

	if hasSlot(after, slots[0].StartsAt) {
		return fmt.Errorf("una reprogramación fallida liberó el espacio original")
	}

	return nil
}

func (s *suite) rescheduleToOtherPractitioner(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 14, 2)
	if err != nil {
		return err
	}

	original, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	// El otro profesional no atiende el servicio de la cita.
	if _, err := s.reschedule(ctx, http.StatusConflict, original.ID, slots[1].StartsAt, practitionerB); err != nil {
		return err
	}

	if _, err := s.reschedule(ctx, http.StatusNotFound, original.ID, slots[1].StartsAt, northPractitioner); err != nil {
		return err
	}

	stored, err := s.getAppointment(ctx, original.ID)
	if err != nil {
		return err
	}

	if stored.Status != "booked" || stored.PractitionerID != practitionerA {
		return fmt.Errorf("la cita cambió después de una reprogramación rechazada: %+v", stored)
	}

	return nil
}

func (s *suite) cancelRecordsReasonAndUser(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 15, 1)
	if err != nil {
		return err
	}

	created, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	path := "/api/v1/appointments/" + created.ID + "/cancel"

	for _, body := range []map[string]string{{}, {"reason": "   "}} {
		if _, err := s.expect(ctx, http.StatusBadRequest, http.MethodPost, path, s.reception, body); err != nil {
			return err
		}
	}

	if _, err := s.expect(ctx, http.StatusForbidden, http.MethodPost, path, s.practitioner, map[string]string{"reason": "No puedo"}); err != nil {
		return err
	}

	resp, err := s.expect(ctx, http.StatusOK, http.MethodPost, path, s.reception, map[string]string{"reason": "El paciente avisó que no puede asistir"})
	if err != nil {
		return err
	}

	var cancelled appointment
	if err := resp.decode(&cancelled); err != nil {
		return err
	}

	if cancelled.Status != "cancelled" {
		return fmt.Errorf("la cita quedó en %s", cancelled.Status)
	}

	if cancelled.CancellationReason == nil || *cancelled.CancellationReason != "El paciente avisó que no puede asistir" {
		return fmt.Errorf("no quedó el motivo de la cancelación: %s", resp)
	}

	if cancelled.ClosedBy == nil || *cancelled.ClosedBy == "" {
		return fmt.Errorf("no quedó registrado quién canceló: %s", resp)
	}

	return nil
}

func (s *suite) cancelFreesTheSlot(ctx context.Context) error {
	date := s.day(16)

	before, _, err := s.slots(ctx, practitionerA, serviceA, date)
	if err != nil {
		return err
	}

	if len(before) < 1 {
		return fmt.Errorf("el día %s no tiene espacios", date)
	}

	target := before[0].StartsAt

	created, err := s.book(ctx, s.patients[0], practitionerA, serviceA, target)
	if err != nil {
		return err
	}

	// Esta consulta deja en caché la agenda con el espacio ocupado.
	taken, _, err := s.slots(ctx, practitionerA, serviceA, date)
	if err != nil {
		return err
	}

	if hasSlot(taken, target) {
		return fmt.Errorf("el espacio reservado sigue apareciendo libre")
	}

	if _, err := s.expect(
		ctx, http.StatusOK, http.MethodPost, "/api/v1/appointments/"+created.ID+"/cancel", s.reception,
		map[string]string{"reason": "Cancelada para probar I7"},
	); err != nil {
		return err
	}

	after, cache, err := s.slots(ctx, practitionerA, serviceA, date)
	if err != nil {
		return err
	}

	if !hasSlot(after, target) {
		return fmt.Errorf("el espacio cancelado no volvió a la disponibilidad (X-Cache=%q)", cache)
	}

	// Y se puede reservar de nuevo de inmediato.
	_, err = s.book(ctx, s.patients[1], practitionerA, serviceA, target)

	return err
}

func (s *suite) finalStatesAreFinal(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 17, 2)
	if err != nil {
		return err
	}

	created, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	base := "/api/v1/appointments/" + created.ID

	if _, err := s.expect(ctx, http.StatusOK, http.MethodPost, base+"/cancel", s.reception, map[string]string{"reason": "Para probar RF13"}); err != nil {
		return err
	}

	transitions := []struct {
		path string
		body map[string]string
	}{
		{base + "/cancel", map[string]string{"reason": "Otra vez"}},
		{base + "/reschedule", map[string]string{"starts_at": slots[1].StartsAt}},
		{base + "/outcome", map[string]string{"outcome": "attended"}},
		{base + "/outcome", map[string]string{"outcome": "no_show"}},
	}

	for _, transition := range transitions {
		if _, err := s.expect(ctx, http.StatusConflict, http.MethodPost, transition.path, s.reception, transition.body); err != nil {
			return err
		}
	}

	stored, err := s.getAppointment(ctx, created.ID)
	if err != nil {
		return err
	}

	if stored.Status != "cancelled" {
		return fmt.Errorf("la cita cancelada pasó a %s", stored.Status)
	}

	return nil
}

func (s *suite) outcomeNotBeforeStart(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 18, 1)
	if err != nil {
		return err
	}

	created, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	path := "/api/v1/appointments/" + created.ID + "/outcome"

	for _, outcome := range []string{"attended", "no_show"} {
		resp, err := s.expect(ctx, http.StatusConflict, http.MethodPost, path, s.reception, map[string]string{"outcome": outcome})
		if err != nil {
			return err
		}

		if !strings.Contains(string(resp.body), "not started") {
			return fmt.Errorf("el 409 no dice que la cita no ha empezado: %s", resp)
		}
	}

	for _, outcome := range []string{"", "cancelled", "booked", "feliz"} {
		if _, err := s.expect(ctx, http.StatusBadRequest, http.MethodPost, path, s.reception, map[string]string{"outcome": outcome}); err != nil {
			return err
		}
	}

	stored, err := s.getAppointment(ctx, created.ID)
	if err != nil {
		return err
	}

	if stored.Status != "booked" {
		return fmt.Errorf("la cita quedó en %s", stored.Status)
	}

	return nil
}

// outcomeAfterStart necesita una cita cuya hora de inicio ya pasó. Como no se
// puede reservar en el pasado, abre el horario a las 24 horas, reserva una
// cita que empieza en tres segundos y espera a que empiece.
func (s *suite) outcomeAfterStart(ctx context.Context) error {
	if err := s.putSchedule(ctx, practitionerA, allDay()); err != nil {
		return err
	}

	defer s.putSchedule(ctx, practitionerA, splitDays())

	bookSoon := func(patientID string) (appointment, error) {
		// Una ejecución anterior pudo dejar una cita en estos mismos
		// minutos; se prueba con inicios cada vez más tardíos.
		var lastErr error

		for attempt := 0; attempt < 5; attempt++ {
			startsAt := time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339)

			created, err := s.book(ctx, patientID, practitionerA, serviceA, startsAt)
			if err == nil {
				return created, nil
			}

			lastErr = err
			time.Sleep(time.Second)
		}

		return appointment{}, lastErr
	}

	attended, err := bookSoon(s.patients[2])
	if err != nil {
		return err
	}

	start, _ := time.Parse(time.RFC3339, attended.StartsAt)
	time.Sleep(time.Until(start) + 1500*time.Millisecond)

	path := "/api/v1/appointments/" + attended.ID + "/outcome"

	// El profesional puede registrar el desenlace (matriz de permisos).
	resp, err := s.expect(ctx, http.StatusOK, http.MethodPost, path, s.practitioner, map[string]string{"outcome": "attended"})
	if err != nil {
		return err
	}

	var closed appointment
	if err := resp.decode(&closed); err != nil {
		return err
	}

	if closed.Status != "attended" || closed.ClosedBy == nil {
		return fmt.Errorf("desenlace inesperado: %s", resp)
	}

	// Atendida es un estado final: no se puede cambiar por ausencia.
	if _, err := s.expect(ctx, http.StatusConflict, http.MethodPost, path, s.reception, map[string]string{"outcome": "no_show"}); err != nil {
		return err
	}

	s.attended = attended.ID

	return nil
}

func (s *suite) availabilityByService(ctx context.Context) error {
	from, to := s.day(20), s.day(22)

	resp, err := s.expect(
		ctx, http.StatusOK, http.MethodGet,
		"/api/v1/availability?service_id="+serviceA+"&from="+from+"&to="+to, s.reception, nil,
	)
	if err != nil {
		return err
	}

	var body struct {
		Items []struct {
			PractitionerID string `json:"practitioner_id"`
			Date           string `json:"date"`
			Slots          []slot `json:"slots"`
		} `json:"items"`
	}
	if err := resp.decode(&body); err != nil {
		return err
	}

	dates := map[string]int{}

	for _, item := range body.Items {
		if item.PractitionerID == practitionerB {
			return fmt.Errorf("aparece un profesional que no atiende el servicio")
		}

		if item.PractitionerID == practitionerA {
			dates[item.Date] = len(item.Slots)
		}
	}

	for _, date := range []string{from, s.day(21), to} {
		if dates[date] == 0 {
			return fmt.Errorf("el profesional no aparece con espacios el %s: %v", date, dates)
		}
	}

	checks := []string{
		"/api/v1/availability?service_id=" + serviceA + "&from=" + to + "&to=" + from,
		"/api/v1/availability?service_id=" + serviceA + "&from=" + from + "&to=" + s.day(60),
		"/api/v1/availability?service_id=" + serviceA + "&from=" + from,
		"/api/v1/availability?from=" + from + "&to=" + to,
	}

	for _, path := range checks {
		if _, err := s.expect(ctx, http.StatusBadRequest, http.MethodGet, path, s.reception, nil); err != nil {
			return err
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Carrera
// ---------------------------------------------------------------------------

// raceForTheLastSlot es la pregunta rectora del proyecto. En cada ronda deja
// un solo espacio libre en el día, suelta raceClients reservas a la vez por
// ese espacio y exige exactamente una concedida (201), todas las demás
// rechazadas por espacio tomado (409) y una sola cita vigente al final.
func (s *suite) raceForTheLastSlot(ctx context.Context) error {
	for round := 0; round < raceRounds; round++ {
		dayOffset := 30 + round

		slots, err := s.freeSlots(ctx, dayOffset, 2)
		if err != nil {
			return err
		}

		// Un bloqueo cubre todo el día menos el primer espacio: ese es "el
		// último espacio de la agenda".
		if _, _, err := s.createBlock(ctx, slots[1].StartsAt, slots[len(slots)-1].EndsAt); err != nil {
			return err
		}

		remaining, err := s.freeSlots(ctx, dayOffset, 1)
		if err != nil {
			return err
		}

		if len(remaining) != 1 {
			return fmt.Errorf("ronda %d: debía quedar un espacio y quedan %d", round+1, len(remaining))
		}

		target := remaining[0].StartsAt

		statuses := make([]int, raceClients)
		bodies := make([]string, raceClients)
		errs := make([]error, raceClients)

		var (
			wg    sync.WaitGroup
			start = make(chan struct{})
		)

		for client := 0; client < raceClients; client++ {
			wg.Add(1)

			go func(client int) {
				defer wg.Done()

				body := bookBody(s.patients[client], practitionerA, serviceA, target)

				// Todos esperan la misma señal para salir a la vez.
				<-start

				resp, err := s.call(ctx, http.MethodPost, "/api/v1/appointments", s.reception, body)
				if err != nil {
					errs[client] = err
					return
				}

				statuses[client] = resp.status
				bodies[client] = string(resp.body)

				if resp.status == http.StatusCreated {
					var created appointment
					if json.Unmarshal(resp.body, &created) == nil {
						s.track(created.ID)
					}
				}
			}(client)
		}

		close(start)
		wg.Wait()

		granted, taken := 0, 0

		for client := 0; client < raceClients; client++ {
			if errs[client] != nil {
				return fmt.Errorf("ronda %d: %w", round+1, errs[client])
			}

			switch {
			case statuses[client] == http.StatusCreated:
				granted++
			case statuses[client] == http.StatusConflict && strings.Contains(bodies[client], "slot already taken"):
				taken++
			default:
				return fmt.Errorf("ronda %d: respuesta inesperada %d %s", round+1, statuses[client], bodies[client])
			}
		}

		if granted != 1 || taken != raceClients-1 {
			return fmt.Errorf("ronda %d: %d reservas concedidas y %d rechazadas; se esperaban 1 y %d", round+1, granted, taken, raceClients-1)
		}

		after, _, err := s.slots(ctx, practitionerA, serviceA, s.day(dayOffset))
		if err != nil {
			return err
		}

		if len(after) != 0 {
			return fmt.Errorf("ronda %d: después de la carrera la agenda todavía ofrece %d espacios", round+1, len(after))
		}
	}

	return nil
}

// raceForTheSamePatient hace competir dos reservas del mismo paciente a la
// misma hora con dos profesionales distintos. Aquí no hay un candado común
// (cada reserva bloquea a su profesional): decide la restricción de exclusión
// del paciente.
func (s *suite) raceForTheSamePatient(ctx context.Context) error {
	for round := 0; round < raceRounds; round++ {
		slots, err := s.freeSlots(ctx, 60+round, 1)
		if err != nil {
			return err
		}

		target := slots[0].StartsAt

		requests := []map[string]string{
			bookBody(s.patients[3], practitionerA, serviceA, target),
			bookBody(s.patients[3], practitionerB, serviceB, target),
		}

		statuses := make([]int, len(requests))

		var (
			wg    sync.WaitGroup
			start = make(chan struct{})
		)

		for i, body := range requests {
			wg.Add(1)

			go func(i int, body map[string]string) {
				defer wg.Done()

				<-start

				resp, err := s.call(ctx, http.MethodPost, "/api/v1/appointments", s.reception, body)
				if err != nil {
					return
				}

				statuses[i] = resp.status

				if resp.status == http.StatusCreated {
					var created appointment
					if json.Unmarshal(resp.body, &created) == nil {
						s.track(created.ID)
					}
				}
			}(i, body)
		}

		close(start)
		wg.Wait()

		granted, rejected := 0, 0

		for _, status := range statuses {
			switch status {
			case http.StatusCreated:
				granted++
			case http.StatusConflict:
				rejected++
			}
		}

		if granted != 1 || rejected != 1 {
			return fmt.Errorf("ronda %d: respuestas %v; se esperaban un 201 y un 409", round+1, statuses)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Aislamiento entre clínicas (I5) y Expediente (RF15)
// ---------------------------------------------------------------------------

func (s *suite) otherClinicIsIsolated(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 90, 2)
	if err != nil {
		return err
	}

	created, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	blockPath, _, err := s.createBlock(ctx, slots[1].StartsAt, slots[1].EndsAt)
	if err != nil {
		return err
	}

	appointmentPath := "/api/v1/appointments/" + created.ID
	practitionerPath := "/api/v1/practitioners/" + practitionerA

	checks := []struct {
		method string
		path   string
		token  string
		body   any
	}{
		{http.MethodGet, appointmentPath, s.northReception, nil},
		{http.MethodPost, appointmentPath + "/cancel", s.northReception, map[string]string{"reason": "Desde otra clínica"}},
		{http.MethodPost, appointmentPath + "/reschedule", s.northReception, map[string]string{"starts_at": slots[1].StartsAt}},
		{http.MethodPost, appointmentPath + "/outcome", s.northReception, map[string]string{"outcome": "no_show"}},
		{http.MethodGet, practitionerPath + "/schedule", s.northReception, nil},
		{http.MethodPut, practitionerPath + "/schedule", s.northAdmin, map[string]any{"blocks": []scheduleBlock{}}},
		{http.MethodGet, practitionerPath + "/blocks", s.northReception, nil},
		{http.MethodPost, practitionerPath + "/blocks", s.northAdmin, map[string]string{"starts_at": slots[0].StartsAt, "ends_at": slots[0].EndsAt, "reason": "Desde otra clínica"}},
		{http.MethodGet, blockPath + "/conflicts", s.northReception, nil},
		{http.MethodDelete, blockPath, s.northAdmin, nil},
		{http.MethodGet, practitionerPath + "/availability?service_id=" + serviceA + "&date=" + s.day(90), s.northReception, nil},
		{http.MethodGet, "/api/v1/availability?service_id=" + serviceA + "&from=" + s.day(90) + "&to=" + s.day(90), s.northReception, nil},
		// Reservar desde Norte con un paciente, un profesional o un servicio
		// de Demo.
		{http.MethodPost, "/api/v1/appointments", s.northReception, bookBody(s.patients[1], practitionerA, serviceA, slots[1].StartsAt)},
	}

	for _, check := range checks {
		if _, err := s.expect(ctx, http.StatusNotFound, check.method, check.path, check.token, check.body); err != nil {
			return err
		}
	}

	// Nada de lo anterior cambió algo en Demo.
	stored, err := s.getAppointment(ctx, created.ID)
	if err != nil {
		return err
	}

	if stored.Status != "booked" {
		return fmt.Errorf("la cita quedó en %s después de los intentos desde otra clínica", stored.Status)
	}

	return s.scheduleRoundTrip(ctx)
}

func (s *suite) forgedClinicIsIgnored(ctx context.Context) error {
	slots, err := s.freeSlots(ctx, 91, 1)
	if err != nil {
		return err
	}

	body := map[string]string{
		"clinic_id":       northClinic,
		"patient_id":      s.patients[0],
		"practitioner_id": practitionerA,
		"service_id":      serviceA,
		"starts_at":       slots[0].StartsAt,
	}

	resp, err := s.expect(ctx, http.StatusCreated, http.MethodPost, "/api/v1/appointments?clinic_id="+northClinic, s.reception, body)
	if err != nil {
		return err
	}

	var created appointment
	if err := resp.decode(&created); err != nil {
		return err
	}

	s.track(created.ID)

	if created.ClinicID != demoClinic {
		return fmt.Errorf("la cita quedó en la clínica %s", created.ClinicID)
	}

	// Norte, pidiendo la clínica de Demo por parámetro, sigue sin verla.
	path := "/api/v1/appointments/" + created.ID + "?clinic_id=" + demoClinic
	if _, err := s.expect(ctx, http.StatusNotFound, http.MethodGet, path, s.northReception, nil); err != nil {
		return err
	}

	path = "/api/v1/practitioners/" + practitionerA + "/availability?clinic_id=" + demoClinic + "&service_id=" + serviceA + "&date=" + s.day(91)
	_, err = s.expect(ctx, http.StatusNotFound, http.MethodGet, path, s.northReception, nil)

	return err
}

func (s *suite) internalRouteIsHidden(ctx context.Context) error {
	if s.attended == "" {
		return fmt.Errorf("la prueba de RF12 no dejó una cita atendida")
	}

	path := "/internal/appointments/" + s.attended + "/clinical-note-context"
	_, err := s.expect(ctx, http.StatusNotFound, http.MethodGet, path, s.practitioner, nil)

	return err
}

// clinicalNoteEndToEnd comprueba el contrato con Expediente: la nota solo se
// acepta si Agenda confirma que la cita está atendida y que la atendió quien
// escribe.
func (s *suite) clinicalNoteEndToEnd(ctx context.Context) error {
	if s.attended == "" {
		return fmt.Errorf("la prueba de RF12 no dejó una cita atendida")
	}

	slots, err := s.freeSlots(ctx, 92, 1)
	if err != nil {
		return err
	}

	pending, err := s.book(ctx, s.patients[0], practitionerA, serviceA, slots[0].StartsAt)
	if err != nil {
		return err
	}

	note := func(appointmentID string) map[string]string {
		return map[string]string{"appointment_id": appointmentID, "content": "Nota de la prueba de integración de citas."}
	}

	// Una cita que todavía no se atiende no admite nota.
	if _, err := s.expect(ctx, http.StatusConflict, http.MethodPost, "/api/v1/clinical-notes", s.practitioner, note(pending.ID)); err != nil {
		return err
	}

	// Otro profesional no puede escribir la nota de una cita ajena.
	if _, err := s.expect(ctx, http.StatusNotFound, http.MethodPost, "/api/v1/clinical-notes", s.practitioner2, note(s.attended)); err != nil {
		return err
	}

	_, err = s.expect(ctx, http.StatusCreated, http.MethodPost, "/api/v1/clinical-notes", s.practitioner, note(s.attended))

	return err
}
