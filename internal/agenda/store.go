package agenda

import (
	"context"
	"errors"
	"fmt"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements CatalogStore on agenda_db.
type PostgresStore struct {
	pool *pgxpool.Pool
	tx   *database.TxManager
}

// NewPostgresStore returns the agenda store.
func NewPostgresStore(pool *pgxpool.Pool, tx *database.TxManager) *PostgresStore {
	return &PostgresStore{pool: pool, tx: tx}
}

// UpsertClinic stores the local projection of a clinic.
func (s *PostgresStore) UpsertClinic(ctx context.Context, clinicID, timezone string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO clinics (id, timezone)
		VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET timezone = EXCLUDED.timezone
	`, clinicID, timezone)

	return database.MapError(err)
}

// DeleteClinic removes the projection of a clinic. Deleting a clinic that is
// already gone succeeds; deleting one that has data is a conflict.
func (s *PostgresStore) DeleteClinic(ctx context.Context, clinicID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM clinics WHERE id = $1`, clinicID)
	if err != nil {
		mapped := database.MapError(err)
		// A foreign key violation here means the clinic is in use.
		if errors.Is(mapped, apperr.ErrNotFound) {
			return apperr.Conflict("the clinic already has data")
		}

		return mapped
	}

	return nil
}

const serviceSelect = `
	SELECT
		s.id::text, s.clinic_id::text, s.name, s.duration_minutes,
		s.specialty_id::text, sp.name, s.active, s.created_at
	FROM services s
	JOIN specialties sp
		ON sp.clinic_id = s.clinic_id
	   AND sp.id = s.specialty_id
`

// ensureSpecialty devuelve el id de la especialidad de la clínica con ese
// nombre y la crea si no existe. El índice único sobre (clinic_id,
// lower(name)) hace la búsqueda insensible a mayúsculas y segura ante
// llamadas concurrentes; el UPDATE sin efecto solo logra que RETURNING
// devuelva la fila.
func ensureSpecialty(ctx context.Context, db database.DBTX, clinicID, name string) (string, error) {
	var specialtyID string

	err := db.QueryRow(ctx, `
		INSERT INTO specialties (clinic_id, name)
		VALUES ($1, $2)
		ON CONFLICT (clinic_id, lower(name))
		DO UPDATE SET name = specialties.name
		RETURNING id::text
	`, clinicID, name).Scan(&specialtyID)
	if err != nil {
		return "", database.MapError(err)
	}

	return specialtyID, nil
}

func scanService(row pgx.Row) (MedicalService, error) {
	var service MedicalService

	err := row.Scan(
		&service.ID,
		&service.ClinicID,
		&service.Name,
		&service.DurationMinutes,
		&service.SpecialtyID,
		&service.Specialty,
		&service.Active,
		&service.CreatedAt,
	)

	return service, err
}

// CreateService inserts a service in the clinic catalog.
func (s *PostgresStore) CreateService(ctx context.Context, clinicID string, in NewMedicalService) (MedicalService, error) {
	var service MedicalService

	err := s.tx.Run(ctx, database.TxOptions{ClinicID: clinicID}, func(ctx context.Context, tx *database.Tx) error {
		specialtyID, err := ensureSpecialty(ctx, tx, clinicID, in.Specialty)
		if err != nil {
			return err
		}

		var serviceID string

		err = tx.QueryRow(ctx, `
			INSERT INTO services (clinic_id, name, duration_minutes, specialty_id)
			VALUES ($1, $2, $3, $4)
			RETURNING id::text
		`, clinicID, in.Name, in.DurationMinutes, specialtyID).Scan(&serviceID)
		if err != nil {
			return database.MapError(err)
		}

		row := tx.QueryRow(ctx, serviceSelect+`
			WHERE s.clinic_id = $1 AND s.id = $2
		`, clinicID, serviceID)

		service, err = scanService(row)
		if err != nil {
			return database.MapError(err)
		}

		return nil
	})
	if err != nil {
		return MedicalService{}, err
	}

	return service, nil
}

// ListServices returns the clinic catalog ordered by name.
func (s *PostgresStore) ListServices(ctx context.Context, clinicID string) ([]MedicalService, error) {
	rows, err := s.pool.Query(ctx, serviceSelect+`
		WHERE s.clinic_id = $1
		ORDER BY s.name
	`, clinicID)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	services := make([]MedicalService, 0)
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}

		services = append(services, service)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return services, nil
}

// GetService returns one service of the clinic.
func (s *PostgresStore) GetService(ctx context.Context, clinicID, serviceID string) (MedicalService, error) {
	row := s.pool.QueryRow(ctx, serviceSelect+`
		WHERE s.clinic_id = $1 AND s.id = $2
	`, clinicID, serviceID)

	service, err := scanService(row)
	if err != nil {
		return MedicalService{}, database.MapError(err)
	}

	return service, nil
}

// DeactivateService marks a service inactive.
func (s *PostgresStore) DeactivateService(ctx context.Context, clinicID, serviceID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE services
		SET active = FALSE
		WHERE clinic_id = $1 AND id = $2
	`, clinicID, serviceID)
	if err != nil {
		return database.MapError(err)
	}

	if tag.RowsAffected() == 0 {
		return apperr.NotFound("service not found")
	}

	return nil
}

// CreatePractitioner inserts the practitioner and their services in one
// transaction.
func (s *PostgresStore) CreatePractitioner(
	ctx context.Context,
	clinicID string,
	in NewPractitioner,
	serviceIDs []string,
) (Practitioner, error) {
	var practitioner Practitioner

	err := s.tx.Run(ctx, database.TxOptions{ClinicID: clinicID}, func(ctx context.Context, tx *database.Tx) error {
		specialtyID, err := ensureSpecialty(ctx, tx, clinicID, in.Specialty)
		if err != nil {
			return err
		}

		var practitionerID string

		err = tx.QueryRow(ctx, `
			INSERT INTO practitioners (clinic_id, user_id, user_subject, display_name, specialty_id)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id::text
		`, clinicID, in.UserID, in.UserSubject, in.DisplayName, specialtyID).Scan(&practitionerID)
		if err != nil {
			return database.MapError(err)
		}

		if err := insertPractitionerServices(ctx, tx, clinicID, practitionerID, specialtyID, serviceIDs); err != nil {
			return err
		}

		practitioner, err = getPractitioner(ctx, tx, clinicID, practitionerID)
		return err
	})
	if err != nil {
		return Practitioner{}, err
	}

	return practitioner, nil
}

// ReplacePractitionerServices replaces the set of services a practitioner
// attends. The practitioner row is locked so two concurrent replacements
// cannot interleave.
func (s *PostgresStore) ReplacePractitionerServices(
	ctx context.Context,
	clinicID string,
	practitionerID string,
	serviceIDs []string,
) (Practitioner, error) {
	var practitioner Practitioner

	err := s.tx.Run(ctx, database.TxOptions{ClinicID: clinicID}, func(ctx context.Context, tx *database.Tx) error {
		var specialtyID string

		err := tx.QueryRow(ctx, `
			SELECT specialty_id::text
			FROM practitioners
			WHERE clinic_id = $1 AND id = $2
			FOR UPDATE
		`, clinicID, practitionerID).Scan(&specialtyID)
		if err != nil {
			return database.MapError(err)
		}

		_, err = tx.Exec(ctx, `
			DELETE FROM practitioner_services
			WHERE clinic_id = $1 AND practitioner_id = $2
		`, clinicID, practitionerID)
		if err != nil {
			return database.MapError(err)
		}

		if err := insertPractitionerServices(ctx, tx, clinicID, practitionerID, specialtyID, serviceIDs); err != nil {
			return err
		}

		practitioner, err = getPractitioner(ctx, tx, clinicID, practitionerID)
		return err
	})
	if err != nil {
		return Practitioner{}, err
	}

	return practitioner, nil
}

// insertPractitionerServices asocia servicios en una sola sentencia. Solo se
// insertan los servicios de la misma clínica, activos y que requieran la
// especialidad del profesional (comparada por id); si falta alguno de los
// servicios pedidos en ese conjunto, toda la operación falla.
func insertPractitionerServices(
	ctx context.Context,
	db database.DBTX,
	clinicID string,
	practitionerID string,
	specialtyID string,
	serviceIDs []string,
) error {
	if len(serviceIDs) == 0 {
		return nil
	}

	tag, err := db.Exec(ctx, `
		INSERT INTO practitioner_services (clinic_id, practitioner_id, service_id)
		SELECT s.clinic_id, $2, s.id
		FROM services s
		WHERE s.clinic_id = $1
		  AND s.id = ANY ($3::text[]::uuid[])
		  AND s.active
		  AND s.specialty_id = $4::uuid
	`, clinicID, practitionerID, serviceIDs, specialtyID)
	if err != nil {
		return database.MapError(err)
	}

	if tag.RowsAffected() != int64(len(serviceIDs)) {
		return apperr.Invalid(
			"every service must exist in the clinic, be active and require the practitioner's specialty",
		)
	}

	return nil
}

const practitionerQuery = `
	SELECT
		p.id::text,
		p.clinic_id::text,
		p.user_id::text,
		p.user_subject,
		p.display_name,
		p.specialty_id::text,
		sp.name,
		p.active,
		p.created_at,
		COALESCE(
			array_agg(ps.service_id::text ORDER BY ps.service_id)
				FILTER (WHERE ps.service_id IS NOT NULL),
			'{}'
		)
	FROM practitioners p
	JOIN specialties sp
		ON sp.clinic_id = p.clinic_id
	   AND sp.id = p.specialty_id
	LEFT JOIN practitioner_services ps
		ON ps.clinic_id = p.clinic_id
	   AND ps.practitioner_id = p.id
	WHERE p.clinic_id = $1
`

func scanPractitioner(row pgx.Row) (Practitioner, error) {
	var practitioner Practitioner

	err := row.Scan(
		&practitioner.ID,
		&practitioner.ClinicID,
		&practitioner.UserID,
		&practitioner.UserSubject,
		&practitioner.DisplayName,
		&practitioner.SpecialtyID,
		&practitioner.Specialty,
		&practitioner.Active,
		&practitioner.CreatedAt,
		&practitioner.ServiceIDs,
	)

	if practitioner.ServiceIDs == nil {
		practitioner.ServiceIDs = []string{}
	}

	return practitioner, err
}

func getPractitioner(ctx context.Context, db database.DBTX, clinicID, practitionerID string) (Practitioner, error) {
	row := db.QueryRow(ctx, practitionerQuery+`
		AND p.id = $2
		GROUP BY p.id, sp.id
	`, clinicID, practitionerID)

	practitioner, err := scanPractitioner(row)
	if err != nil {
		return Practitioner{}, database.MapError(err)
	}

	return practitioner, nil
}

// GetPractitioner returns one practitioner of the clinic with their services.
func (s *PostgresStore) GetPractitioner(ctx context.Context, clinicID, practitionerID string) (Practitioner, error) {
	return getPractitioner(ctx, s.pool, clinicID, practitionerID)
}

// ListPractitioners returns the practitioners of the clinic with their
// services, ordered by name.
func (s *PostgresStore) ListPractitioners(ctx context.Context, clinicID string) ([]Practitioner, error) {
	rows, err := s.pool.Query(ctx, practitionerQuery+`
		GROUP BY p.id, sp.id
		ORDER BY p.display_name
	`, clinicID)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	practitioners := make([]Practitioner, 0)
	for rows.Next() {
		practitioner, err := scanPractitioner(rows)
		if err != nil {
			return nil, fmt.Errorf("scan practitioner: %w", err)
		}

		practitioners = append(practitioners, practitioner)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return practitioners, nil
}
