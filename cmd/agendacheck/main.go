// Command agendacheck ejecuta las pruebas de integración de Agenda contra la
// pila real de Compose. Hoy cubre RF14 (pacientes), RF18 (auditoría de
// contacto), RF16 (historial del paciente), RF20 (agenda del día), RF21
// (conteo de citas por estado) e I5 (aislamiento entre clínicas). Cada prueba lleva en su nombre el requisito o
// invariante que sostiene, para la trazabilidad.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	demoClinicID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	northClinicID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	unknownPatientID = "99999999-9999-4999-8999-999999999999"

	// auditTimeout es lo máximo que se espera a que el relay entregue los
	// eventos a Auditoría.
	auditTimeout = 40 * time.Second
)

// patient es la respuesta de las rutas de pacientes.
type patient struct {
	ID             string `json:"id"`
	ClinicID       string `json:"clinic_id"`
	Identification string `json:"identification"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	BirthDate      string `json:"birth_date"`
	Phone          string `json:"phone"`
	Email          string `json:"email"`
	Address        string `json:"address"`
	Active         bool   `json:"active"`
}

// listResponse es la respuesta de los listados. Los elementos se leen como
// mapas para poder comprobar qué campos traen.
type listResponse struct {
	Items []map[string]any `json:"items"`
}

// auditEntry es un registro de la bitácora central (RF19).
type auditEntry struct {
	ClinicID      string `json:"clinic_id"`
	ActorUsername string `json:"actor_username"`
	Action        string `json:"action"`
	PatientID     string `json:"patient_id"`
	ResourceType  string `json:"resource_type"`
	SourceService string `json:"source_service"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

// suite guarda los tokens y el estado que comparten las pruebas. Las pruebas
// corren en orden y cada una puede usar lo que crearon las anteriores.
type suite struct {
	apiURL      string
	keycloakURL string
	http        *http.Client

	reception      string
	admin          string
	practitioner   string
	platformAdmin  string
	northReception string
	northAdmin     string
	southReception string
	southAdmin     string
	practitioner2  string

	// runID hace únicas las identificaciones de cada ejecución, para que las
	// pruebas se puedan repetir sin chocar con datos de ejecuciones
	// anteriores.
	runID string

	demoPatientID  string
	otherPatientID string
	northPatientID string

	// Cuántos accesos de lectura y escritura debe tener registrados el
	// paciente de Demo. Al final se comparan con la bitácora de auditoría.
	expectedReads  int
	expectedWrites int
}

func main() {
	s := &suite{
		apiURL:      envOr("API_URL", "http://proxy:8080"),
		keycloakURL: envOr("KEYCLOAK_URL", "http://keycloak:8080"),
		http:        &http.Client{Timeout: 10 * time.Second},
		runID:       fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000_000),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	if err := s.login(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}

	failed := s.run(ctx)

	fmt.Println()

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %d prueba(s) de integración de Agenda fallaron\n", failed)
		os.Exit(1)
	}

	fmt.Println("All Agenda integration checks passed.")
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return strings.TrimRight(value, "/")
}

// login obtiene un token de cada usuario de prueba.
func (s *suite) login(ctx context.Context) error {
	users := []struct {
		target   *string
		username string
		password string
	}{
		{&s.reception, "reception", "Reception123!"},
		{&s.admin, "clinic.admin", "Clinic123!"},
		{&s.practitioner, "practitioner", "Practitioner123!"},
		{&s.platformAdmin, "platform.admin", "Platform123!"},
		{&s.northReception, "norte.reception", "Norte123!"},
		{&s.northAdmin, "norte.admin", "Norte123!"},
		{&s.southReception, "sur.reception", "Sur123!"},
		{&s.southAdmin, "sur.admin", "Sur123!"},
		{&s.practitioner2, "practitioner2", "Practitioner123!"},
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
		token, err := s.tokenOnce(ctx, form)
		if err == nil {
			return token, nil
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

func (s *suite) tokenOnce(ctx context.Context, form url.Values) (string, error) {
	endpoint := s.keycloakURL + "/realms/medicitas/protocol/openid-connect/token"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Keycloak respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return "", err
	}

	if strings.TrimSpace(token.AccessToken) == "" {
		return "", fmt.Errorf("Keycloak no devolvió access_token")
	}

	return token.AccessToken, nil
}

// call hace una petición a la API y devuelve el código y el cuerpo de la
// respuesta. El cuerpo se envía como JSON en UTF-8, así se comprueban también
// las tildes.
func (s *suite) call(ctx context.Context, method, path, token, body string) (int, []byte, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.apiURL+path, reader)
	if err != nil {
		return 0, nil, err
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}

	return resp.StatusCode, raw, nil
}

// expect hace una petición y falla si el código de estado no es el esperado.
func (s *suite) expect(ctx context.Context, method, path, token, body string, want int) ([]byte, error) {
	status, raw, err := s.call(ctx, method, path, token, body)
	if err != nil {
		return nil, err
	}

	if status != want {
		return raw, fmt.Errorf("%s %s: status = %d, want %d, body = %s", method, path, status, want, strings.TrimSpace(string(raw)))
	}

	return raw, nil
}

// patientBody arma el cuerpo de un alta con la identificación indicada.
// Incluye tildes a propósito, para comprobar que se guardan bien.
func patientBody(identification string) string {
	body, _ := json.Marshal(map[string]string{
		"identification": identification,
		"first_name":     "María",
		"last_name":      "Pérez Ñandú",
		"birth_date":     "1990-05-12",
		"phone":          "+506 8888-1111",
		"email":          "maria@example.com",
		"address":        "Cartago",
	})

	return string(body)
}

// identification devuelve una identificación única de esta ejecución.
func (s *suite) identification(suffix string) string {
	return "IT-" + s.runID + "-" + suffix
}

func decodePatient(raw []byte) (patient, error) {
	var result patient
	err := json.Unmarshal(raw, &result)

	return result, err
}

// getPatient lee un paciente y, si es el de Demo y la lectura se concede, la
// cuenta como acceso auditado.
func (s *suite) getPatient(ctx context.Context, token, id string) (patient, error) {
	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/patients/"+id, token, "", http.StatusOK)
	if err != nil {
		return patient{}, err
	}

	if id == s.demoPatientID {
		s.expectedReads++
	}

	return decodePatient(raw)
}

// listIDs devuelve los ids de un listado de pacientes.
func listIDs(raw []byte) ([]string, []map[string]any, error) {
	var list listResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil, err
	}

	ids := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		id, _ := item["id"].(string)
		ids = append(ids, id)
	}

	return ids, list.Items, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}

	return false
}

// need falla con un mensaje claro si una prueba anterior no dejó el estado que
// esta necesita.
func need(value, what string) error {
	if value == "" {
		return fmt.Errorf("falta %s: falló una prueba anterior de la que esta depende", what)
	}

	return nil
}

// run ejecuta todas las pruebas en orden y devuelve cuántas fallaron.
func (s *suite) run(ctx context.Context) int {
	tests := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"RF14 recepción registra un paciente y se conservan las tildes (201)", s.createPatient},
		{"RF14 administración registra otro paciente (201)", s.createSecondPatient},
		{"RF14 el profesional no puede registrar pacientes (403)", s.practitionerCannotCreate},
		{"RF14 la administración de plataforma no accede a pacientes (403)", s.platformAdminCannotAccess},
		{"RF17 sin token no se accede a pacientes (401)", s.noTokenIsRejected},
		{"RF14 una identificación repetida en la misma clínica es conflicto (409)", s.duplicateIdentification},
		{"RF14 las entradas inválidas se rechazan (400)", s.invalidInputs},
		{"I5 la misma identificación en otra clínica es válida (201)", s.sameIdentificationInOtherClinic},
		{"I5 un clinic_id falsificado en el cuerpo se ignora", s.forgedClinicInBody},
		{"RF14 consulta de un paciente con su contacto (200)", s.getOwnPatient},
		{"RF14 id mal formado (400) e id inexistente (404)", s.badAndUnknownIDs},
		{"RF14 el profesional puede leer pacientes (200)", s.practitionerCanRead},
		{"RF14 actualiza un dato, borra otro y conserva el resto", s.updatePatient},
		{"RF14 las actualizaciones inválidas se rechazan (400)", s.invalidUpdates},
		{"RF14 no se puede actualizar a una identificación existente (409)", s.updateToDuplicateIdentification},
		{"RF14 el profesional no puede actualizar pacientes (403)", s.practitionerCannotUpdate},
		{"I5 otra clínica no ve el paciente (404)", s.otherClinicCannotRead},
		{"I5 otra clínica no puede modificar el paciente (404)", s.otherClinicCannotUpdate},
		{"I5 los listados no cruzan clínicas, ni con parámetros manipulados", s.listsDoNotCrossClinics},
		{"RF14 el listado no expone datos de contacto", s.listHasNoContact},
		{"RF14 búsqueda por prefijo, límite y comodines literales", s.searchAndLimit},
		{"RF18 cada acceso al contacto queda en la bitácora, y solo esos", s.auditTrailIsExact},
		{"I5 la bitácora de otra clínica no muestra esos eventos", s.otherClinicCannotSeeAudit},
		{"RF16 el historial trae las citas del paciente, de la más reciente a la más antigua", s.historyOfAPatient},
		{"RF16 el historial muestra el motivo de cancelación y la reprogramación", s.historyShowsCancellationAndReschedule},
		{"RF16 un paciente sin citas tiene un historial vacío (200)", s.historyOfAPatientWithoutAppointments},
		{"RF16 el historial admite un límite", s.historyLimit},
		{"RF16 el historial lo leen los tres roles de la clínica (el profesional también)", s.historyPermissions},
		{"RF16 las entradas inválidas se rechazan (400) y un paciente inexistente es 404", s.historyInvalidInputs},
		{"RF16 el historial muestra las horas en la zona horaria de la clínica", s.historyTimezone},
		{"I5 el historial no cruza clínicas", s.historyDoesNotCrossClinics},
		{"RF20 recepción ve la agenda de toda la clínica, sin contacto de pacientes", s.agendaClinicWide},
		{"RF20 la agenda de un profesional solo trae sus citas", s.agendaOfPractitioner},
		{"RF20 la agenda se filtra por estado", s.agendaStatusFilter},
		{"RF20 el profesional solo ve su propia agenda (403 con la de otro)", s.practitionerSeesOnlyOwnAgenda},
		{"RF20 las entradas inválidas se rechazan (400) y un profesional inexistente es 404", s.agendaInvalidInputs},
		{"RF20 cada clínica ve las horas en su zona horaria", s.agendaTimezone},
		{"RF17 la agenda exige token (401) y el rol adecuado (403)", s.agendaPermissions},
		{"I5 la agenda no cruza clínicas, ni con parámetros manipulados", s.agendaDoesNotCrossClinics},
		{"RF21 el conteo por estado de cada clínica coincide con la semilla", s.countsMatchTheSeed},
		{"RF21 el rango incluye sus dos extremos y los estados sin citas salen en cero", s.countsRangeAndZeros},
		{"RF21 las entradas inválidas se rechazan (400)", s.countsInvalidInputs},
		{"RF17 el conteo exige token (401) y es solo de administración (403)", s.countsPermissions},
		{"I5 el conteo ignora una clínica enviada en los parámetros", s.countsIgnoreForgedClinic},
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

func (s *suite) createPatient(ctx context.Context) error {
	raw, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.reception, patientBody(s.identification("A")), http.StatusCreated)
	if err != nil {
		return err
	}

	created, err := decodePatient(raw)
	if err != nil {
		return err
	}

	switch {
	case created.ID == "":
		return fmt.Errorf("la respuesta no trae id")
	case created.ClinicID != demoClinicID:
		return fmt.Errorf("clinic_id = %q, want la clínica del token %q", created.ClinicID, demoClinicID)
	case created.FirstName != "María" || created.LastName != "Pérez Ñandú":
		return fmt.Errorf("las tildes no se conservaron: %q %q", created.FirstName, created.LastName)
	case created.BirthDate != "1990-05-12":
		return fmt.Errorf("birth_date = %q, want 1990-05-12", created.BirthDate)
	case created.Phone != "+506 8888-1111" || created.Email != "maria@example.com" || created.Address != "Cartago":
		return fmt.Errorf("el contacto no se devolvió completo: %+v", created)
	case !created.Active:
		return fmt.Errorf("un paciente nuevo debe estar activo")
	}

	s.demoPatientID = created.ID
	s.expectedWrites++

	return nil
}

func (s *suite) createSecondPatient(ctx context.Context) error {
	raw, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.admin, patientBody(s.identification("B")), http.StatusCreated)
	if err != nil {
		return err
	}

	created, err := decodePatient(raw)
	if err != nil {
		return err
	}

	s.otherPatientID = created.ID

	return nil
}

func (s *suite) practitionerCannotCreate(ctx context.Context) error {
	_, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.practitioner, patientBody(s.identification("P")), http.StatusForbidden)

	return err
}

func (s *suite) platformAdminCannotAccess(ctx context.Context) error {
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/patients", s.platformAdmin, "", http.StatusForbidden); err != nil {
		return err
	}

	_, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.platformAdmin, patientBody(s.identification("PA")), http.StatusForbidden)

	return err
}

func (s *suite) noTokenIsRejected(ctx context.Context) error {
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/patients", "", "", http.StatusUnauthorized); err != nil {
		return err
	}

	_, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", "", patientBody(s.identification("NT")), http.StatusUnauthorized)

	return err
}

func (s *suite) duplicateIdentification(ctx context.Context) error {
	_, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.reception, patientBody(s.identification("A")), http.StatusConflict)

	return err
}

func (s *suite) invalidInputs(ctx context.Context) error {
	bodies := map[string]string{
		"sin nombre":            `{"identification":"X-1","last_name":"A","birth_date":"1990-01-01"}`,
		"sin identificación":    `{"first_name":"A","last_name":"B","birth_date":"1990-01-01"}`,
		"fecha futura":          `{"identification":"X-2","first_name":"A","last_name":"B","birth_date":"2999-01-01"}`,
		"fecha anterior a 1900": `{"identification":"X-3","first_name":"A","last_name":"B","birth_date":"1899-12-31"}`,
		"formato de fecha":      `{"identification":"X-4","first_name":"A","last_name":"B","birth_date":"12/05/1990"}`,
		"correo inválido":       `{"identification":"X-5","first_name":"A","last_name":"B","birth_date":"1990-01-01","email":"no-es-correo"}`,
		"teléfono inválido":     `{"identification":"X-6","first_name":"A","last_name":"B","birth_date":"1990-01-01","phone":"abc"}`,
		"JSON mal formado":      `{no es json`,
	}

	for name, body := range bodies {
		if _, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.reception, body, http.StatusBadRequest); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

func (s *suite) sameIdentificationInOtherClinic(ctx context.Context) error {
	raw, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.northReception, patientBody(s.identification("A")), http.StatusCreated)
	if err != nil {
		return err
	}

	created, err := decodePatient(raw)
	if err != nil {
		return err
	}

	if created.ClinicID != northClinicID {
		return fmt.Errorf("clinic_id = %q, want %q", created.ClinicID, northClinicID)
	}

	s.northPatientID = created.ID

	return nil
}

func (s *suite) forgedClinicInBody(ctx context.Context) error {
	body := fmt.Sprintf(
		`{"identification":%q,"first_name":"A","last_name":"B","birth_date":"1990-01-01","clinic_id":%q}`,
		s.identification("F"), northClinicID,
	)

	raw, err := s.expect(ctx, http.MethodPost, "/api/v1/patients", s.reception, body, http.StatusCreated)
	if err != nil {
		return err
	}

	created, err := decodePatient(raw)
	if err != nil {
		return err
	}

	if created.ClinicID != demoClinicID {
		return fmt.Errorf("clinic_id = %q: la clínica salió del cuerpo y no del token", created.ClinicID)
	}

	return nil
}

func (s *suite) getOwnPatient(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	got, err := s.getPatient(ctx, s.reception, s.demoPatientID)
	if err != nil {
		return err
	}

	if got.ID != s.demoPatientID || got.Phone != "+506 8888-1111" || got.Email != "maria@example.com" {
		return fmt.Errorf("el paciente no se devolvió con su contacto: %+v", got)
	}

	return nil
}

func (s *suite) badAndUnknownIDs(ctx context.Context) error {
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/patients/1-OR-1=1", s.reception, "", http.StatusBadRequest); err != nil {
		return err
	}

	_, err := s.expect(ctx, http.MethodGet, "/api/v1/patients/"+unknownPatientID, s.reception, "", http.StatusNotFound)

	return err
}

func (s *suite) practitionerCanRead(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	_, err := s.getPatient(ctx, s.practitioner, s.demoPatientID)

	return err
}

func (s *suite) updatePatient(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	if _, err := s.expect(ctx, http.MethodPatch, "/api/v1/patients/"+s.demoPatientID, s.reception, `{"phone":"2222-3333","email":""}`, http.StatusOK); err != nil {
		return err
	}

	s.expectedWrites++

	got, err := s.getPatient(ctx, s.reception, s.demoPatientID)
	if err != nil {
		return err
	}

	switch {
	case got.Phone != "2222-3333":
		return fmt.Errorf("phone = %q, want 2222-3333", got.Phone)
	case got.Email != "":
		return fmt.Errorf("email = %q, want vacío (se envió vacío para borrarlo)", got.Email)
	case got.Address != "Cartago" || got.FirstName != "María" || got.BirthDate != "1990-05-12":
		return fmt.Errorf("los campos no enviados deben conservarse: %+v", got)
	}

	return nil
}

func (s *suite) invalidUpdates(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	for _, body := range []string{`{}`, `{"first_name":"  "}`, `{"birth_date":"2999-01-01"}`, `{"email":"no-es-correo"}`} {
		if _, err := s.expect(ctx, http.MethodPatch, "/api/v1/patients/"+s.demoPatientID, s.reception, body, http.StatusBadRequest); err != nil {
			return fmt.Errorf("cuerpo %s: %w", body, err)
		}
	}

	return nil
}

func (s *suite) updateToDuplicateIdentification(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	body := fmt.Sprintf(`{"identification":%q}`, s.identification("B"))

	_, err := s.expect(ctx, http.MethodPatch, "/api/v1/patients/"+s.demoPatientID, s.reception, body, http.StatusConflict)

	return err
}

func (s *suite) practitionerCannotUpdate(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	_, err := s.expect(ctx, http.MethodPatch, "/api/v1/patients/"+s.demoPatientID, s.practitioner, `{"phone":"1"}`, http.StatusForbidden)

	return err
}

func (s *suite) otherClinicCannotRead(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	for _, token := range []string{s.northReception, s.northAdmin} {
		if _, err := s.expect(ctx, http.MethodGet, "/api/v1/patients/"+s.demoPatientID, token, "", http.StatusNotFound); err != nil {
			return err
		}
	}

	return nil
}

func (s *suite) otherClinicCannotUpdate(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	if _, err := s.expect(ctx, http.MethodPatch, "/api/v1/patients/"+s.demoPatientID, s.northReception, `{"phone":"0000"}`, http.StatusNotFound); err != nil {
		return err
	}

	// El intento ajeno no debe haber cambiado nada.
	got, err := s.getPatient(ctx, s.reception, s.demoPatientID)
	if err != nil {
		return err
	}

	if got.Phone != "2222-3333" {
		return fmt.Errorf("phone = %q: otra clínica modificó el paciente", got.Phone)
	}

	return nil
}

func (s *suite) listsDoNotCrossClinics(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	if err := need(s.northPatientID, "el paciente de Norte"); err != nil {
		return err
	}

	demoRaw, err := s.expect(ctx, http.MethodGet, "/api/v1/patients?limit=200", s.reception, "", http.StatusOK)
	if err != nil {
		return err
	}

	demoIDs, _, err := listIDs(demoRaw)
	if err != nil {
		return err
	}

	if !contains(demoIDs, s.demoPatientID) || contains(demoIDs, s.northPatientID) {
		return fmt.Errorf("el listado de Demo debe tener a su paciente y no al de Norte")
	}

	// La clínica de Norte pide el listado, incluso intentando forzar la
	// clínica de Demo con parámetros.
	queries := []string{
		"/api/v1/patients?limit=200",
		"/api/v1/patients?limit=200&clinic_id=" + demoClinicID,
		"/api/v1/patients?limit=200&clinicId=" + demoClinicID,
	}

	for _, query := range queries {
		raw, err := s.expect(ctx, http.MethodGet, query, s.northReception, "", http.StatusOK)
		if err != nil {
			return err
		}

		ids, _, err := listIDs(raw)
		if err != nil {
			return err
		}

		if !contains(ids, s.northPatientID) {
			return fmt.Errorf("%s: el listado de Norte debe tener a su paciente", query)
		}

		if contains(ids, s.demoPatientID) || contains(ids, s.otherPatientID) {
			return fmt.Errorf("%s: el listado de Norte muestra pacientes de Demo", query)
		}
	}

	// Buscar desde Norte una identificación que solo existe en Demo no debe
	// encontrar nada.
	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/patients?q="+url.QueryEscape(s.identification("B")), s.northReception, "", http.StatusOK)
	if err != nil {
		return err
	}

	ids, _, err := listIDs(raw)
	if err != nil {
		return err
	}

	if len(ids) != 0 {
		return fmt.Errorf("la búsqueda desde Norte encontró %d paciente(s) de Demo", len(ids))
	}

	return nil
}

func (s *suite) listHasNoContact(ctx context.Context) error {
	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/patients?limit=200", s.reception, "", http.StatusOK)
	if err != nil {
		return err
	}

	_, items, err := listIDs(raw)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		return fmt.Errorf("el listado vino vacío")
	}

	for _, item := range items {
		for _, field := range []string{"phone", "email", "address"} {
			if _, present := item[field]; present {
				return fmt.Errorf("el listado expone el campo de contacto %q", field)
			}
		}
	}

	return nil
}

func (s *suite) searchAndLimit(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	// Prefijo de la identificación, sin distinguir mayúsculas.
	prefix := strings.ToLower(s.identification("A"))

	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/patients?q="+url.QueryEscape(prefix), s.reception, "", http.StatusOK)
	if err != nil {
		return err
	}

	ids, _, err := listIDs(raw)
	if err != nil {
		return err
	}

	if len(ids) != 1 || ids[0] != s.demoPatientID {
		return fmt.Errorf("la búsqueda por prefijo debe encontrar exactamente al paciente, encontró %v", ids)
	}

	// El límite se respeta.
	raw, err = s.expect(ctx, http.MethodGet, "/api/v1/patients?limit=1", s.reception, "", http.StatusOK)
	if err != nil {
		return err
	}

	ids, _, err = listIDs(raw)
	if err != nil {
		return err
	}

	if len(ids) != 1 {
		return fmt.Errorf("limit=1 devolvió %d pacientes", len(ids))
	}

	// Un límite que no es un número se rechaza.
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/patients?limit=abc", s.reception, "", http.StatusBadRequest); err != nil {
		return err
	}

	// El comodín % se busca como texto, no como "todo".
	raw, err = s.expect(ctx, http.MethodGet, "/api/v1/patients?q=%25", s.reception, "", http.StatusOK)
	if err != nil {
		return err
	}

	ids, _, err = listIDs(raw)
	if err != nil {
		return err
	}

	if len(ids) != 0 {
		return fmt.Errorf("q=%% devolvió %d pacientes: el comodín no se tomó literalmente", len(ids))
	}

	return nil
}

// auditEntries consulta la bitácora de la clínica del token filtrando por
// paciente.
func (s *suite) auditEntries(ctx context.Context, token, patientID string) ([]auditEntry, error) {
	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/audit?limit=500&patient_id="+patientID, token, "", http.StatusOK)
	if err != nil {
		return nil, err
	}

	var response struct {
		Items []auditEntry `json:"items"`
	}

	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}

	return response.Items, nil
}

func countActions(entries []auditEntry) (reads, writes int) {
	for _, entry := range entries {
		switch entry.Action {
		case "patient_contact.read":
			reads++
		case "patient_contact.write":
			writes++
		}
	}

	return reads, writes
}

// auditTrailIsExact comprueba que la bitácora tiene exactamente los accesos
// que se hicieron. Los eventos llegan de forma asíncrona, así que primero
// espera a que el relay los entregue y después vuelve a consultar, para
// comprobar que no aparece ningún evento de más. Las operaciones rechazadas
// (409, 403, 404) no deben haber dejado eventos.
func (s *suite) auditTrailIsExact(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	deadline := time.Now().Add(auditTimeout)

	var entries []auditEntry

	for {
		var err error

		entries, err = s.auditEntries(ctx, s.admin, s.demoPatientID)
		if err != nil {
			return err
		}

		reads, writes := countActions(entries)
		if reads >= s.expectedReads && writes >= s.expectedWrites {
			break
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("la bitácora no recibió a tiempo los eventos: lecturas %d/%d, escrituras %d/%d", reads, s.expectedReads, writes, s.expectedWrites)
		}

		time.Sleep(time.Second)
	}

	// Margen para que un evento de más, si existiera, alcance a llegar.
	time.Sleep(4 * time.Second)

	entries, err := s.auditEntries(ctx, s.admin, s.demoPatientID)
	if err != nil {
		return err
	}

	reads, writes := countActions(entries)
	if reads != s.expectedReads || writes != s.expectedWrites {
		return fmt.Errorf("eventos del paciente: lecturas %d (esperadas %d), escrituras %d (esperadas %d)", reads, s.expectedReads, writes, s.expectedWrites)
	}

	for _, entry := range entries {
		switch {
		case entry.ClinicID != demoClinicID:
			return fmt.Errorf("evento de la clínica %q en la bitácora de Demo", entry.ClinicID)
		case entry.SourceService != "agenda" || entry.ResourceType != "patient_contact":
			return fmt.Errorf("evento con origen %q y recurso %q inesperados", entry.SourceService, entry.ResourceType)
		case strings.HasPrefix(entry.ActorUsername, "norte."):
			return fmt.Errorf("hay un evento del usuario %q, que no logró acceder al paciente", entry.ActorUsername)
		}
	}

	return nil
}

func (s *suite) otherClinicCannotSeeAudit(ctx context.Context) error {
	if err := need(s.demoPatientID, "el paciente de Demo"); err != nil {
		return err
	}

	entries, err := s.auditEntries(ctx, s.northAdmin, s.demoPatientID)
	if err != nil {
		return err
	}

	if len(entries) != 0 {
		return fmt.Errorf("la bitácora de Norte muestra %d evento(s) de un paciente de Demo", len(entries))
	}

	return nil
}
