package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// snapshot lee los recursos de ejemplo de una clínica con sus propios tokens
// Devuelve el texto de las respuestas, para comparar antes y después de los
// intentos de acceso ajeno
// Si algún recurso no responde 200, falla: así se sabe que las rutas existen
func (s *suite) snapshot(ctx context.Context, c *clinic) (string, error) {
	reads := []struct {
		path  string
		token string
	}{
		{"/api/v1/services/" + c.serviceID, c.admin},
		{"/api/v1/practitioners/" + c.practitionerID, c.admin},
		{"/api/v1/patients/" + c.patientID, c.reception},
		{"/api/v1/users/" + c.receptionUserID, c.admin},
	}

	var combined strings.Builder

	for _, read := range reads {
		raw, err := s.expect(ctx, http.MethodGet, read.path, read.token, "", http.StatusOK)
		if err != nil {
			return "", fmt.Errorf("Clínica %s: %w", c.name, err)
		}

		combined.Write(raw)
	}

	return combined.String(), nil
}

// ownResourcesWork es el control de todas las demás pruebas
// Cada clínica debe poder leer sus propios recursos en los cuatro servicios
func (s *suite) ownResourcesWork(ctx context.Context) error {
	for _, c := range s.clinics {
		if _, err := s.snapshot(ctx, c); err != nil {
			return err
		}

		// Tenancy: el token firmado dice a qué clínica pertenece quien llama
		raw, err := s.expect(ctx, http.MethodGet, "/api/v1/me", c.admin, "", http.StatusOK)
		if err != nil {
			return err
		}

		var me struct {
			ClinicID string `json:"clinic_id"`
		}

		if err := json.Unmarshal(raw, &me); err != nil || me.ClinicID != c.id {
			return fmt.Errorf("Clínica %s: /me trae la clínica %q, want %q", c.name, me.ClinicID, c.id)
		}

		// Agenda: el día con más citas de la semilla
		if _, err := s.expect(ctx, http.MethodGet, "/api/v1/agenda?date=2026-11-16", c.reception, "", http.StatusOK); err != nil {
			return err
		}

		// Expediente: la nota de la semilla la lee su dueño
		raw, err = s.expect(ctx, http.MethodGet, "/api/v1/clinical-notes/"+c.noteID, c.practitioner, "", http.StatusOK)
		if err != nil {
			return err
		}

		var note struct {
			ClinicID string `json:"clinic_id"`
			Content  string `json:"content"`
		}

		if err := json.Unmarshal(raw, &note); err != nil || note.ClinicID != c.id || note.Content == "" {
			return fmt.Errorf("Clínica %s: la nota %s trae clinic_id %q, want %q", c.name, c.noteID, note.ClinicID, c.id)
		}

		// Auditoría: la consulta del administrador funciona
		if _, err := s.expect(ctx, http.MethodGet, "/api/v1/audit?limit=1", c.admin, "", http.StatusOK); err != nil {
			return err
		}
	}

	return nil
}

func (s *suite) noTokenIsRejected(ctx context.Context) error {
	demo := s.clinics[0]

	paths := []string{
		"/api/v1/me",
		"/api/v1/users",
		"/api/v1/platform/clinics",
		"/api/v1/services",
		"/api/v1/practitioners/" + demo.practitionerID,
		"/api/v1/patients",
		"/api/v1/patients/" + demo.patientID + "/history",
		"/api/v1/agenda?date=2026-11-16",
		"/api/v1/reports/appointments-by-status?from=2026-11-09&to=2026-11-14",
		"/api/v1/clinical-notes/" + demo.noteID,
		"/api/v1/audit",
	}

	for _, path := range paths {
		// Sin token
		if _, err := s.expect(ctx, http.MethodGet, path, "", "", http.StatusUnauthorized); err != nil {
			return err
		}

		// Con un token que no es de Keycloak
		if _, err := s.expect(ctx, http.MethodGet, path, "esto.no.es-un-token", "", http.StatusUnauthorized); err != nil {
			return err
		}
	}

	return nil
}

func (s *suite) internalRoutesAreHidden(ctx context.Context) error {
	demo := s.clinics[0]

	// Estas rutas son entre servicios y el proxy no las deja pasar
	// Ni siquiera con el token de un administrador
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/internal/audit/events", `{}`},
		{http.MethodPut, "/internal/clinics/" + demo.id, `{"timezone":"America/Costa_Rica"}`},
		{http.MethodDelete, "/internal/clinics/" + demo.id, ""},
		{http.MethodGet, "/internal/appointments/a1a1a1a1-0000-4000-8000-000000000001/clinical-note-context", ""},
		{http.MethodPut, "/internal/practitioners/by-user/" + demo.receptionUserID + "/active", `{"active":false}`},
	}

	for _, route := range routes {
		for _, token := range []string{demo.admin, s.platformAdmin, ""} {
			if _, err := s.expect(ctx, route.method, route.path, token, route.body, http.StatusNotFound); err != nil {
				return err
			}
		}
	}

	return nil
}
