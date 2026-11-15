// Command leakcheck ejecuta las pruebas de fuga entre clínicas (I5)
// Corre contra la pila real de Compose, con tokens reales de Keycloak
//
// Cubre los cuatro servicios: Tenancy, Agenda, Expediente y Auditoría
// Cada prueba intenta alcanzar datos de otra clínica por rutas, ids ajenos,
// cuerpos y filtros manipulados
// Todas las pruebas se apoyan en las semillas de seed/
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

// clinic reúne los tokens y los recursos de ejemplo de una clínica
// Los ids son los de seed/tenancy.sql, seed/agenda.sql y seed/expediente.sql
type clinic struct {
	name string
	id   string

	// Tokens de los usuarios de la clínica
	admin        string
	reception    string
	practitioner string

	// Usuarios de Tenancy
	receptionUserID string
	usernames       []string

	// Recursos de Agenda
	serviceID      string
	practitionerID string
	patientID      string

	// Nota clínica de Expediente y su dueño
	noteID string
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

// suite guarda los tokens y el estado que comparten las pruebas
type suite struct {
	apiURL      string
	keycloakURL string
	http        *http.Client

	clinics []*clinic

	// El segundo profesional de Demo no es dueño de la nota 1 de su clínica
	demoPractitioner2 string
	platformAdmin     string

	// runID hace único el usuario que crea la prueba de alta
	runID string
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
		fmt.Fprintf(os.Stderr, "FAIL: %d prueba(s) de fuga entre clínicas fallaron\n", failed)
		os.Exit(1)
	}

	fmt.Println("All tenant isolation leak checks passed.")
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return strings.TrimRight(value, "/")
}

// login obtiene un token de cada usuario y arma la tabla de clínicas
func (s *suite) login(ctx context.Context) error {
	s.clinics = []*clinic{
		{
			name: "Demo", id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			receptionUserID: "33333333-aaaa-4333-8333-333333333333",
			usernames:       []string{"clinic.admin", "reception", "practitioner", "practitioner2", "practitioner3"},
			serviceID:       "d1d1d1d1-0000-4000-8000-000000000001",
			practitionerID:  "c1c1c1c1-0000-4000-8000-000000000001",
			patientID:       "b1b1b1b1-0000-4000-8000-000000000001",
			noteID:          "0a1a1a1a-0000-4000-8000-000000000001",
		},
		{
			name: "Norte", id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
			receptionUserID: "66666666-aaaa-4666-8666-666666666666",
			usernames:       []string{"norte.admin", "norte.reception", "norte.practitioner1", "norte.practitioner2", "norte.practitioner3"},
			serviceID:       "d2d2d2d2-0000-4000-8000-000000000002",
			practitionerID:  "c2c2c2c2-0000-4000-8000-000000000001",
			patientID:       "b2b2b2b2-0000-4000-8000-000000000001",
			noteID:          "0a2a2a2a-0000-4000-8000-000000000001",
		},
		{
			name: "Sur", id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
			receptionUserID: "40404040-aaaa-4404-8404-404040404040",
			usernames:       []string{"sur.admin", "sur.reception", "sur.practitioner1", "sur.practitioner2", "sur.practitioner3"},
			serviceID:       "d3d3d3d3-0000-4000-8000-000000000001",
			practitionerID:  "c3c3c3c3-0000-4000-8000-000000000001",
			patientID:       "b3b3b3b3-0000-4000-8000-000000000001",
			noteID:          "0a3a3a3a-0000-4000-8000-000000000001",
		},
	}

	users := []struct {
		target   *string
		username string
		password string
	}{
		{&s.clinics[0].admin, "clinic.admin", "Clinic123!"},
		{&s.clinics[0].reception, "reception", "Reception123!"},
		{&s.clinics[0].practitioner, "practitioner", "Practitioner123!"},
		{&s.demoPractitioner2, "practitioner2", "Practitioner123!"},
		{&s.clinics[1].admin, "norte.admin", "Norte123!"},
		{&s.clinics[1].reception, "norte.reception", "Norte123!"},
		{&s.clinics[1].practitioner, "norte.practitioner1", "Norte123!"},
		{&s.clinics[2].admin, "sur.admin", "Sur123!"},
		{&s.clinics[2].reception, "sur.reception", "Sur123!"},
		{&s.clinics[2].practitioner, "sur.practitioner1", "Sur123!"},
		{&s.platformAdmin, "platform.admin", "Platform123!"},
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

// token pide un token a Keycloak
// Reintenta porque Keycloak puede tardar unos segundos en aceptar peticiones
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

// call hace una petición a la API y devuelve el código y el cuerpo
// headers es opcional y sirve para enviar cabeceras manipuladas
func (s *suite) call(ctx context.Context, method, path, token, body string, headers map[string]string) (int, []byte, error) {
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

	for name, value := range headers {
		req.Header.Set(name, value)
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

// expect hace una petición y falla si el código no es el esperado
func (s *suite) expect(ctx context.Context, method, path, token, body string, want int) ([]byte, error) {
	status, raw, err := s.call(ctx, method, path, token, body, nil)
	if err != nil {
		return nil, err
	}

	if status != want {
		return raw, fmt.Errorf("%s %s: status = %d, want %d, body = %s", method, path, status, want, strings.TrimSpace(string(raw)))
	}

	return raw, nil
}

// variant es una forma de intentar manipular la clínica de la petición
type variant struct {
	name    string
	path    string
	headers map[string]string
}

// variants devuelve la petición normal y dos intentos de forzar otra clínica
// Uno agrega clinic_id a la consulta y el otro envía la cabecera X-Clinic-Id
func variants(path, forgedClinicID string) []variant {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}

	return []variant{
		{"sin manipular", path, nil},
		{"con clinic_id en la consulta", path + separator + "clinic_id=" + forgedClinicID, nil},
		{"con la cabecera X-Clinic-Id", path, map[string]string{"X-Clinic-Id": forgedClinicID}},
	}
}

// items lee la lista "items" de una respuesta
func items(raw []byte) ([]map[string]any, error) {
	var response struct {
		Items []map[string]any `json:"items"`
	}

	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("respuesta no válida: %w", err)
	}

	return response.Items, nil
}

// field lee un campo de texto de un elemento
func field(item map[string]any, name string) string {
	value, _ := item[name].(string)

	return value
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}

	return false
}

// others devuelve las clínicas distintas de la indicada
func (s *suite) others(own *clinic) []*clinic {
	result := make([]*clinic, 0, len(s.clinics)-1)

	for _, c := range s.clinics {
		if c != own {
			result = append(result, c)
		}
	}

	return result
}

// run ejecuta las pruebas en orden y devuelve cuántas fallaron
// Las primeras son los controles: sin ellas, un 404 podría significar que la
// ruta no existe y no que la clínica está aislada
func (s *suite) run(ctx context.Context) int {
	tests := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"control: cada clínica accede a sus propios recursos en los cuatro servicios (200)", s.ownResourcesWork},
		{"RF17 sin token o con un token falso, las rutas de los cuatro servicios responden 401", s.noTokenIsRejected},
		{"I5 las rutas internas no se exponen por el proxy (404)", s.internalRoutesAreHidden},
		{"I5 Tenancy: cada administrador lista solo a los usuarios de su clínica", s.tenancyListsAreScoped},
		{"I5 Tenancy: un clinic_id falsificado al crear un usuario se ignora", s.tenancyForgedCreate},
		{"I5 Tenancy: solo la plataforma lista las clínicas", s.tenancyPlatformOnly},
		{"I5 Agenda: los listados de servicios, profesionales y pacientes no traen datos de otra clínica", s.agendaListsAreScoped},
		{"I5 Agenda: un profesional no puede atender un servicio de otra clínica", s.agendaForeignService},
		{"I5 matriz de las tres clínicas: ningún recurso es visible ni modificable desde otra (404)", s.crossClinicMatrix},
		{"I5 Expediente: solo el dueño lee una nota, no otro profesional ni otros roles", s.notesOnlyForTheOwner},
		{"I5 Auditoría: cada administrador ve solo eventos de su clínica, también con filtros manipulados", s.auditIsScoped},
		{"RF19 Auditoría: solo el administrador de la clínica la consulta", s.auditRoles},
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
