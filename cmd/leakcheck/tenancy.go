package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// foreignUsernames reúne los usuarios de ejemplo de las otras clínicas
func (s *suite) foreignUsernames(own *clinic) []string {
	var names []string

	for _, other := range s.others(own) {
		names = append(names, other.usernames...)
	}

	return names
}

func (s *suite) tenancyListsAreScoped(ctx context.Context) error {
	for _, c := range s.clinics {
		foreign := s.foreignUsernames(c)

		for _, other := range s.others(c) {
			for _, v := range variants("/api/v1/users", other.id) {
				status, raw, err := s.call(ctx, http.MethodGet, v.path, c.admin, "", v.headers)
				if err != nil {
					return err
				}

				if status != http.StatusOK {
					return fmt.Errorf("Clínica %s, %s: status = %d, want 200", c.name, v.name, status)
				}

				users, err := items(raw)
				if err != nil {
					return err
				}

				var names []string

				for _, user := range users {
					names = append(names, field(user, "username"))

					// El registro trae su clínica, y debe ser la del token
					if clinicID := field(user, "clinic_id"); clinicID != "" && clinicID != c.id {
						return fmt.Errorf("Clínica %s, %s: el usuario %s es de la clínica %s", c.name, v.name, field(user, "username"), clinicID)
					}
				}

				if !contains(names, c.usernames[0]) {
					return fmt.Errorf("Clínica %s, %s: falta su administrador %s en %v", c.name, v.name, c.usernames[0], names)
				}

				for _, name := range foreign {
					if contains(names, name) {
						return fmt.Errorf("Clínica %s, %s: aparece el usuario %s de otra clínica", c.name, v.name, name)
					}
				}
			}
		}
	}

	return nil
}

func (s *suite) tenancyForgedCreate(ctx context.Context) error {
	demo, north := s.clinics[0], s.clinics[1]
	username := "fuga-" + s.runID

	// El cuerpo, la consulta y la cabecera piden la Clínica Norte
	// El alta debe quedar en la clínica del token, que es Demo
	body := fmt.Sprintf(
		`{"username":%q,"email":%q,"first_name":"Fuga","last_name":"Prueba","password":"Fuga123!xy","role":"reception","clinic_id":%q}`,
		username, username+"@medicitas.local", north.id,
	)

	status, raw, err := s.call(ctx, http.MethodPost, "/api/v1/users?clinic_id="+north.id, demo.admin, body, map[string]string{"X-Clinic-Id": north.id})
	if err != nil {
		return err
	}

	if status != http.StatusCreated {
		return fmt.Errorf("POST /api/v1/users: status = %d, want 201, body = %s", status, raw)
	}

	var created struct {
		ID       string `json:"id"`
		ClinicID string `json:"clinic_id"`
	}

	if err := json.Unmarshal(raw, &created); err != nil {
		return err
	}

	// La prueba no deja usuarios activos: se da de baja al terminar
	defer func() {
		_, _, _ = s.call(ctx, http.MethodDelete, "/api/v1/users/"+created.ID, demo.admin, "", nil)
	}()

	if created.ClinicID != demo.id {
		return fmt.Errorf("el usuario quedó en la clínica %q, want %q (la del token)", created.ClinicID, demo.id)
	}

	// Aparece en la lista de Demo y no en la de Norte
	for _, check := range []struct {
		clinic *clinic
		want   bool
	}{{demo, true}, {north, false}} {
		listRaw, err := s.expect(ctx, http.MethodGet, "/api/v1/users", check.clinic.admin, "", http.StatusOK)
		if err != nil {
			return err
		}

		users, err := items(listRaw)
		if err != nil {
			return err
		}

		found := false

		for _, user := range users {
			if field(user, "username") == username {
				found = true
			}
		}

		if found != check.want {
			return fmt.Errorf("el usuario creado aparece en la lista de %s: %t, want %t", check.clinic.name, found, check.want)
		}
	}

	return nil
}

func (s *suite) tenancyPlatformOnly(ctx context.Context) error {
	// Los roles de clínica no pueden listar las clínicas de la plataforma
	for _, c := range s.clinics {
		for _, token := range []string{c.admin, c.reception, c.practitioner} {
			if _, err := s.expect(ctx, http.MethodGet, "/api/v1/platform/clinics", token, "", http.StatusForbidden); err != nil {
				return err
			}
		}
	}

	// La administración de la plataforma sí las ve, porque ese es su trabajo
	raw, err := s.expect(ctx, http.MethodGet, "/api/v1/platform/clinics", s.platformAdmin, "", http.StatusOK)
	if err != nil {
		return err
	}

	clinics, err := items(raw)
	if err != nil {
		return err
	}

	if len(clinics) < len(s.clinics) {
		return fmt.Errorf("la plataforma ve %d clínicas, want al menos %d", len(clinics), len(s.clinics))
	}

	return nil
}
