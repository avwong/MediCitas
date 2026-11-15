package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// auditWait es lo máximo que se espera a que el relay entregue los eventos
const auditWait = 45 * time.Second

// waitForNoteRead espera a que la bitácora de la clínica tenga la lectura de su
// nota clínica
// El evento lo escribió Expediente al leerla, y el relay lo entrega a Auditoría
func (s *suite) waitForNoteRead(ctx context.Context, c *clinic) error {
	deadline := time.Now().Add(auditWait)

	for {
		raw, err := s.expect(ctx, http.MethodGet, "/api/v1/audit?limit=500", c.admin, "", http.StatusOK)
		if err != nil {
			return err
		}

		events, err := items(raw)
		if err != nil {
			return err
		}

		for _, event := range events {
			if field(event, "resource_id") == c.noteID {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("la bitácora de %s no recibió la lectura de su nota %s en %s", c.name, c.noteID, auditWait)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *suite) auditIsScoped(ctx context.Context) error {
	// Las pruebas anteriores leyeron las notas de las tres clínicas
	// Primero se espera a que esos eventos lleguen a cada bitácora
	for _, c := range s.clinics {
		if err := s.waitForNoteRead(ctx, c); err != nil {
			return err
		}
	}

	for _, c := range s.clinics {
		foreignNotes := s.foreignIDs(c, func(o *clinic) string { return o.noteID })
		foreignPatients := s.foreignIDs(c, func(o *clinic) string { return o.patientID })

		for _, other := range s.others(c) {
			for _, v := range variants("/api/v1/audit?limit=500", other.id) {
				status, raw, err := s.call(ctx, http.MethodGet, v.path, c.admin, "", v.headers)
				if err != nil {
					return err
				}

				if status != http.StatusOK {
					return fmt.Errorf("bitácora de %s, %s: status = %d, want 200", c.name, v.name, status)
				}

				events, err := items(raw)
				if err != nil {
					return err
				}

				for _, event := range events {
					if clinicID := field(event, "clinic_id"); clinicID != c.id {
						return fmt.Errorf("bitácora de %s, %s: el evento %s es de la clínica %s", c.name, v.name, field(event, "event_id"), clinicID)
					}

					if contains(foreignNotes, field(event, "resource_id")) || contains(foreignPatients, field(event, "patient_id")) {
						return fmt.Errorf("bitácora de %s, %s: trae el evento %s de un recurso de otra clínica", c.name, v.name, field(event, "event_id"))
					}
				}
			}

			// Filtrar por un paciente o por un usuario de otra clínica no encuentra nada
			filters := []string{
				"/api/v1/audit?limit=500&patient_id=" + other.patientID,
				"/api/v1/audit?limit=500&user=" + other.usernames[0],
			}

			for _, path := range filters {
				raw, err := s.expect(ctx, http.MethodGet, path, c.admin, "", http.StatusOK)
				if err != nil {
					return err
				}

				events, err := items(raw)
				if err != nil {
					return err
				}

				if len(events) != 0 {
					return fmt.Errorf("bitácora de %s, %s: devolvió %d eventos, want 0", c.name, path, len(events))
				}
			}
		}
	}

	return nil
}

func (s *suite) auditRoles(ctx context.Context) error {
	for _, c := range s.clinics {
		// Solo el administrador de la clínica consulta la bitácora
		for _, token := range []string{c.reception, c.practitioner} {
			if _, err := s.expect(ctx, http.MethodGet, "/api/v1/audit", token, "", http.StatusForbidden); err != nil {
				return err
			}
		}
	}

	// La administración de la plataforma no está en la matriz de la bitácora
	_, err := s.expect(ctx, http.MethodGet, "/api/v1/audit", s.platformAdmin, "", http.StatusForbidden)

	return err
}
