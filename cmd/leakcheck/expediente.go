package main

import (
	"context"
	"fmt"
	"net/http"
)

// secondDemoNoteID es la nota de la semilla que escribió el segundo profesional
// de Demo
const secondDemoNoteID = "0a1a1a1a-0000-4000-8000-000000000002"

func (s *suite) notesOnlyForTheOwner(ctx context.Context) error {
	demo := s.clinics[0]

	// Control: cada profesional lee su propia nota
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/clinical-notes/"+secondDemoNoteID, s.demoPractitioner2, "", http.StatusOK); err != nil {
		return err
	}

	for _, c := range s.clinics {
		path := "/api/v1/clinical-notes/" + c.noteID

		// Los roles que no escriben notas no las leen, aunque sean de su clínica
		for _, token := range []string{c.admin, c.reception, s.platformAdmin} {
			if _, err := s.expect(ctx, http.MethodGet, path, token, "", http.StatusForbidden); err != nil {
				return err
			}
		}

		// Los profesionales de las otras clínicas no la ven
		for _, other := range s.others(c) {
			for _, v := range variants(path, c.id) {
				status, raw, err := s.call(ctx, http.MethodGet, v.path, other.practitioner, "", v.headers)
				if err != nil {
					return err
				}

				if status != http.StatusNotFound {
					return fmt.Errorf("el profesional de %s, %s: la nota de %s dio %d, want 404, body = %s", other.name, v.name, c.name, status, raw)
				}
			}
		}
	}

	// Otro profesional de la misma clínica tampoco ve una nota que no es suya
	// La nota 1 es del primer profesional de Demo y la 2 del segundo
	if _, err := s.expect(ctx, http.MethodGet, "/api/v1/clinical-notes/"+demo.noteID, s.demoPractitioner2, "", http.StatusNotFound); err != nil {
		return err
	}

	_, err := s.expect(ctx, http.MethodGet, "/api/v1/clinical-notes/"+secondDemoNoteID, demo.practitioner, "", http.StatusNotFound)

	return err
}
