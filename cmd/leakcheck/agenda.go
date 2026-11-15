package main

import (
	"context"
	"fmt"
	"net/http"
)

// foreignIDs reúne los ids de ejemplo de las otras clínicas
func (s *suite) foreignIDs(own *clinic, pick func(*clinic) string) []string {
	var ids []string

	for _, other := range s.others(own) {
		ids = append(ids, pick(other))
	}

	return ids
}

func (s *suite) agendaListsAreScoped(ctx context.Context) error {
	lists := []struct {
		name string
		path string
		// ownID es el recurso de ejemplo que la lista debe traer
		ownID func(*clinic) string
		// hasClinic indica si cada elemento trae su clinic_id
		hasClinic bool
	}{
		{"servicios", "/api/v1/services", func(c *clinic) string { return c.serviceID }, true},
		{"profesionales", "/api/v1/practitioners", func(c *clinic) string { return c.practitionerID }, true},
		{"pacientes", "/api/v1/patients?limit=200", func(c *clinic) string { return c.patientID }, false},
	}

	for _, list := range lists {
		for _, c := range s.clinics {
			foreign := s.foreignIDs(c, list.ownID)

			for _, other := range s.others(c) {
				for _, v := range variants(list.path, other.id) {
					status, raw, err := s.call(ctx, http.MethodGet, v.path, c.reception, "", v.headers)
					if err != nil {
						return err
					}

					if status != http.StatusOK {
						return fmt.Errorf("%s de %s, %s: status = %d, want 200", list.name, c.name, v.name, status)
					}

					elements, err := items(raw)
					if err != nil {
						return err
					}

					var ids []string

					for _, element := range elements {
						ids = append(ids, field(element, "id"))

						if clinicID := field(element, "clinic_id"); list.hasClinic && clinicID != c.id {
							return fmt.Errorf("%s de %s, %s: el elemento %s es de la clínica %q", list.name, c.name, v.name, field(element, "id"), clinicID)
						}
					}

					if !contains(ids, list.ownID(c)) {
						return fmt.Errorf("%s de %s, %s: falta %s", list.name, c.name, v.name, list.ownID(c))
					}

					for _, id := range foreign {
						if contains(ids, id) {
							return fmt.Errorf("%s de %s, %s: aparece %s de otra clínica", list.name, c.name, v.name, id)
						}
					}
				}
			}
		}
	}

	return nil
}

func (s *suite) agendaForeignService(ctx context.Context) error {
	demo, north := s.clinics[0], s.clinics[1]

	before, err := s.expect(ctx, http.MethodGet, "/api/v1/practitioners/"+demo.practitionerID, demo.admin, "", http.StatusOK)
	if err != nil {
		return err
	}

	// Asignar a un profesional de Demo un servicio de Norte se rechaza
	body := fmt.Sprintf(`{"service_ids":[%q]}`, north.serviceID)
	if _, err := s.expect(ctx, http.MethodPut, "/api/v1/practitioners/"+demo.practitionerID+"/services", demo.admin, body, http.StatusBadRequest); err != nil {
		return err
	}

	// Registrar un profesional de Demo ligado a un usuario de Norte también
	createBody := fmt.Sprintf(
		`{"user_id":%q,"display_name":"Fuga Prueba","specialty":"Medicina general","service_ids":[%q]}`,
		north.receptionUserID, north.serviceID,
	)
	if _, err := s.expect(ctx, http.MethodPost, "/api/v1/practitioners", demo.admin, createBody, http.StatusNotFound); err != nil {
		return err
	}

	// Ningún intento cambió al profesional
	after, err := s.expect(ctx, http.MethodGet, "/api/v1/practitioners/"+demo.practitionerID, demo.admin, "", http.StatusOK)
	if err != nil {
		return err
	}

	if string(before) != string(after) {
		return fmt.Errorf("el profesional cambió:\nantes:   %s\ndespués: %s", before, after)
	}

	return nil
}

// probe es un intento de alcanzar un recurso de otra clínica
type probe struct {
	name   string
	method string
	path   string
	// kind dice con qué rol se hace el intento
	kind string
	body string
}

// probesFor arma los intentos contra los recursos de la clínica indicada
// Hay lecturas y modificaciones
func probesFor(target *clinic) []probe {
	return []probe{
		{"leer un servicio", http.MethodGet, "/api/v1/services/" + target.serviceID, "admin", ""},
		{"dar de baja un servicio", http.MethodDelete, "/api/v1/services/" + target.serviceID, "admin", ""},
		{"leer un profesional", http.MethodGet, "/api/v1/practitioners/" + target.practitionerID, "admin", ""},
		{"cambiar los servicios de un profesional", http.MethodPut, "/api/v1/practitioners/" + target.practitionerID + "/services", "admin",
			fmt.Sprintf(`{"service_ids":[%q]}`, target.serviceID)},
		{"leer un paciente", http.MethodGet, "/api/v1/patients/" + target.patientID, "reception", ""},
		{"modificar un paciente", http.MethodPatch, "/api/v1/patients/" + target.patientID, "reception", `{"phone":"0000"}`},
		{"leer el historial de un paciente", http.MethodGet, "/api/v1/patients/" + target.patientID + "/history", "reception", ""},
		{"ver la agenda de un profesional", http.MethodGet, "/api/v1/agenda?date=2026-11-16&practitioner_id=" + target.practitionerID, "reception", ""},
		{"leer un usuario", http.MethodGet, "/api/v1/users/" + target.receptionUserID, "admin", ""},
		{"cambiar el rol de un usuario", http.MethodPatch, "/api/v1/users/" + target.receptionUserID + "/role", "admin", `{"role":"practitioner"}`},
		{"dar de baja a un usuario", http.MethodDelete, "/api/v1/users/" + target.receptionUserID, "admin", ""},
		{"leer una nota clínica", http.MethodGet, "/api/v1/clinical-notes/" + target.noteID, "practitioner", ""},
	}
}

// tokenFor devuelve el token del rol indicado dentro de la clínica
func (c *clinic) tokenFor(kind string) string {
	switch kind {
	case "admin":
		return c.admin
	case "reception":
		return c.reception
	default:
		return c.practitioner
	}
}

// crossClinicMatrix prueba todas las parejas de clínicas
// Desde cada clínica intenta leer y modificar los recursos de las otras dos
// Cada intento se repite con un clinic_id y con una cabecera manipulados
func (s *suite) crossClinicMatrix(ctx context.Context) error {
	before := make(map[*clinic]string, len(s.clinics))

	for _, c := range s.clinics {
		snapshot, err := s.snapshot(ctx, c)
		if err != nil {
			return err
		}

		before[c] = snapshot
	}

	attempts := 0

	for _, from := range s.clinics {
		for _, target := range s.others(from) {
			for _, p := range probesFor(target) {
				for _, v := range variants(p.path, target.id) {
					status, raw, err := s.call(ctx, p.method, v.path, from.tokenFor(p.kind), p.body, v.headers)
					if err != nil {
						return err
					}

					attempts++

					if status != http.StatusNotFound {
						return fmt.Errorf("desde %s, %s de %s (%s): status = %d, want 404, body = %s", from.name, p.name, target.name, v.name, status, raw)
					}
				}
			}
		}
	}

	// Ningún intento cambió los recursos de la clínica atacada
	for _, c := range s.clinics {
		after, err := s.snapshot(ctx, c)
		if err != nil {
			return err
		}

		if after != before[c] {
			return fmt.Errorf("los recursos de la Clínica %s cambiaron después de %d intentos ajenos", c.name, attempts)
		}
	}

	return nil
}
