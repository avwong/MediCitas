// Command availabilitybench mide la consulta de disponibilidad (RF7 y RF8)
// contra la pila en ejecución. Sirve para comparar la respuesta con la caché
// y sin ella: se ejecuta una vez con Agenda conectada a Redis y otra con
// Agenda arrancada sin REDIS_ADDR. El procedimiento está en
// docs/concurrencia-y-cache.md.
//
// Fija por un momento el horario de un profesional de la clínica Demo y lo
// restaura al terminar. No crea citas.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Identificadores fijos de la semilla (seed/agenda.sql).
const (
	practitionerID = "c1c1c1c1-0000-4000-8000-000000000001"
	serviceID      = "d1d1d1d1-0000-4000-8000-000000000001"
)

type scheduleBlock struct {
	Weekday   int    `json:"weekday"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

type bench struct {
	apiURL      string
	keycloakURL string
	http        *http.Client
}

func main() {
	b := &bench{
		apiURL:      envOr("API_URL", "http://proxy:8080"),
		keycloakURL: envOr("KEYCLOAK_URL", "http://keycloak:8080"),
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{MaxIdleConnsPerHost: 64},
		},
	}

	if err := b.run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return strings.TrimRight(value, "/")
}

func (b *bench) run() error {
	admin, err := b.token("clinic.admin", "Clinic123!")
	if err != nil {
		return err
	}

	reception, err := b.token("reception", "Reception123!")
	if err != nil {
		return err
	}

	schedulePath := "/api/v1/practitioners/" + practitionerID + "/schedule"

	// Se guarda el horario actual para restaurarlo al final.
	var saved struct {
		Blocks []scheduleBlock `json:"blocks"`
	}

	if status, _, body, err := b.call(http.MethodGet, schedulePath, admin, nil); err != nil || status != http.StatusOK {
		return fmt.Errorf("leer el horario: %d %s %v", status, body, err)
	} else if err := json.Unmarshal(body, &saved); err != nil {
		return err
	}

	blocks := make([]scheduleBlock, 0, 14)
	for weekday := 1; weekday <= 7; weekday++ {
		blocks = append(blocks,
			scheduleBlock{Weekday: weekday, StartTime: "08:00", EndTime: "12:00"},
			scheduleBlock{Weekday: weekday, StartTime: "13:00", EndTime: "17:00"},
		)
	}

	if status, _, body, err := b.call(http.MethodPut, schedulePath, admin, map[string]any{"blocks": blocks}); err != nil || status != http.StatusOK {
		return fmt.Errorf("fijar el horario: %d %s %v", status, body, err)
	}

	defer func() {
		if saved.Blocks == nil {
			saved.Blocks = []scheduleBlock{}
		}

		b.call(http.MethodPut, schedulePath, admin, map[string]any{"blocks": saved.Blocks})
	}()

	day := time.Now().AddDate(0, 0, 30)
	date := day.Format("2006-01-02")

	byPractitioner := "/api/v1/practitioners/" + practitionerID + "/availability?service_id=" + serviceID + "&date=" + date
	byService := "/api/v1/availability?service_id=" + serviceID + "&from=" + date + "&to=" + day.AddDate(0, 0, 30).Format("2006-01-02")

	// La segunda consulta idéntica dice si Agenda está usando la caché.
	b.call(http.MethodGet, byPractitioner, reception, nil)

	_, header, _, err := b.call(http.MethodGet, byPractitioner, reception, nil)
	if err != nil {
		return err
	}

	mode := "sin caché"
	if header.Get("X-Cache") == "hit" {
		mode = "con caché"
	}

	fmt.Printf("Agenda responde %s (X-Cache de la segunda consulta: %q)\n\n", mode, header.Get("X-Cache"))

	measurements := []struct {
		name    string
		path    string
		total   int
		clients int
	}{
		{"RF7, un cliente", byPractitioner, 3000, 1},
		{"RF7, 16 clientes a la vez", byPractitioner, 6000, 16},
		{"RF8 (31 días), un cliente", byService, 300, 1},
	}

	for _, m := range measurements {
		if err := b.measure(mode+": "+m.name, m.path, reception, m.total, m.clients); err != nil {
			return err
		}
	}

	return nil
}

// measure hace total peticiones repartidas entre clients clientes simultáneos
// y resume los tiempos de respuesta.
func (b *bench) measure(name, path, token string, total, clients int) error {
	durations := make([]time.Duration, total)
	jobs := make(chan int, total)

	for i := 0; i < total; i++ {
		jobs <- i
	}
	close(jobs)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)

	started := time.Now()

	for client := 0; client < clients; client++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range jobs {
				begin := time.Now()

				status, _, body, err := b.call(http.MethodGet, path, token, nil)
				if err == nil && status != http.StatusOK {
					err = fmt.Errorf("%d %s", status, body)
				}

				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()

					return
				}

				durations[i] = time.Since(begin)
			}
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	elapsed := time.Since(started)

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}

	fmt.Printf(
		"%-40s n=%-5d media=%6.2f ms  p50=%6.2f ms  p95=%6.2f ms  %6.0f pet/s\n",
		name,
		total,
		millis(sum/time.Duration(total)),
		millis(durations[total/2]),
		millis(durations[total*95/100]),
		float64(total)/elapsed.Seconds(),
	)

	return nil
}

func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// token pide un token a Keycloak.
func (b *bench) token(username, password string) (string, error) {
	resp, err := b.http.PostForm(b.keycloakURL+"/realms/medicitas/protocol/openid-connect/token", url.Values{
		"grant_type": {"password"},
		"client_id":  {"medicitas-cli"},
		"username":   {username},
		"password":   {password},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.AccessToken == "" {
		return "", fmt.Errorf("Keycloak no devolvió un token para %s (%d)", username, resp.StatusCode)
	}

	return body.AccessToken, nil
}

// call hace una petición a la API y devuelve el código, los encabezados y el
// cuerpo de la respuesta.
func (b *bench) call(method, path, token string, body any) (int, http.Header, []byte, error) {
	var reader io.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, b.apiURL+path, reader)
	if err != nil {
		return 0, nil, nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.http.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	return resp.StatusCode, resp.Header, raw, err
}
