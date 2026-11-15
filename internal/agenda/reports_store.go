package agenda

import (
	"context"

	"github.com/avwong/MediCitas/internal/platform/database"
)

// PostgresReportStore implementa ReportStore sobre agenda_db. No guarda el
// pool: cada método usa la transacción que le pasa el servicio.
type PostgresReportStore struct{}

// NewPostgresReportStore devuelve el almacén de la agenda y los reportes.
func NewPostgresReportStore() *PostgresReportStore {
	return &PostgresReportStore{}
}

// Se verifica en compilación que el almacén cumple la interfaz.
var _ ReportStore = (*PostgresReportStore)(nil)

// ClinicTimezone devuelve la zona horaria de la clínica.
func (s *PostgresReportStore) ClinicTimezone(
	ctx context.Context,
	db database.DBTX,
	clinicID string,
) (string, error) {
	var timezone string

	err := db.QueryRow(ctx, `
		SELECT timezone
		FROM clinics
		WHERE id = $1
	`, clinicID).Scan(&timezone)
	if err != nil {
		return "", database.MapError(err)
	}

	return timezone, nil
}

// PractitionerIDBySubject devuelve el profesional ligado al usuario de Keycloak.
func (s *PostgresReportStore) PractitionerIDBySubject(
	ctx context.Context,
	db database.DBTX,
	clinicID, subject string,
) (string, error) {
	var practitionerID string

	err := db.QueryRow(ctx, `
		SELECT id::text
		FROM practitioners
		WHERE clinic_id = $1 AND user_subject = $2
	`, clinicID, subject).Scan(&practitionerID)
	if err != nil {
		return "", database.MapError(err)
	}

	return practitionerID, nil
}

// PractitionerExists indica si el profesional existe en la clínica. Uno de otra
// clínica cuenta como inexistente.
func (s *PostgresReportStore) PractitionerExists(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID string,
) (bool, error) {
	var exists bool

	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM practitioners
			WHERE clinic_id = $1 AND id = $2
		)
	`, clinicID, practitionerID).Scan(&exists)
	if err != nil {
		return false, database.MapError(err)
	}

	return exists, nil
}

// Agenda devuelve las citas cuyo inicio cae dentro del día pedido, ordenadas
// por hora de inicio.
//
// El día va de la medianoche a la medianoche siguiente en la zona de la
// clínica. Se calcula con AT TIME ZONE en la base, que también resuelve bien los
// cambios de horario. El paciente sale sin datos de contacto. Las uniones usan
// las llaves compuestas (clinic_id, id), igual que las llaves foráneas.
func (s *PostgresReportStore) Agenda(
	ctx context.Context,
	db database.DBTX,
	clinicID, timezone string,
	query AgendaQuery,
) ([]AgendaEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT
			a.id::text, a.starts_at, a.ends_at, a.status,
			p.id::text, p.identification, p.first_name, p.last_name,
			pr.id::text, pr.display_name,
			s.id::text, s.name, s.duration_minutes
		FROM appointments a
		JOIN patients p
			ON p.clinic_id = a.clinic_id AND p.id = a.patient_id
		JOIN practitioners pr
			ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id
		JOIN services s
			ON s.clinic_id = a.clinic_id AND s.id = a.service_id
		WHERE a.clinic_id = $1
		  AND a.starts_at >= $2::text::date::timestamp AT TIME ZONE $3
		  AND a.starts_at < ($2::text::date + 1)::timestamp AT TIME ZONE $3
		  AND ($4::text = '' OR a.practitioner_id = NULLIF($4::text, '')::uuid)
		  AND ($5::text = '' OR a.status = $5::text)
		ORDER BY a.starts_at, pr.display_name, a.id
	`, clinicID, query.Date, timezone, query.PractitionerID, query.Status)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	items := make([]AgendaEntry, 0)

	for rows.Next() {
		var entry AgendaEntry

		err := rows.Scan(
			&entry.ID,
			&entry.StartsAt,
			&entry.EndsAt,
			&entry.Status,
			&entry.Patient.ID,
			&entry.Patient.Identification,
			&entry.Patient.FirstName,
			&entry.Patient.LastName,
			&entry.Practitioner.ID,
			&entry.Practitioner.DisplayName,
			&entry.Service.ID,
			&entry.Service.Name,
			&entry.Service.DurationMinutes,
		)
		if err != nil {
			return nil, database.MapError(err)
		}

		items = append(items, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return items, nil
}

// CountByStatus cuenta las citas por estado con una sola consulta agregada. El
// rango va desde la medianoche de la fecha inicial hasta la medianoche posterior
// a la fecha final, ambas en la zona de la clínica. Solo devuelve los estados
// que tienen citas.
func (s *PostgresReportStore) CountByStatus(
	ctx context.Context,
	db database.DBTX,
	clinicID, timezone, from, to string,
) (map[string]int, error) {
	rows, err := db.Query(ctx, `
		SELECT status, count(*)::int
		FROM appointments
		WHERE clinic_id = $1
		  AND starts_at >= $2::text::date::timestamp AT TIME ZONE $4
		  AND starts_at < ($3::text::date + 1)::timestamp AT TIME ZONE $4
		GROUP BY status
	`, clinicID, from, to, timezone)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	counts := make(map[string]int)

	for rows.Next() {
		var (
			status string
			count  int
		)

		if err := rows.Scan(&status, &count); err != nil {
			return nil, database.MapError(err)
		}

		counts[status] = count
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return counts, nil
}

// Patient devuelve los datos básicos de un paciente de la clínica, sin
// contacto. Uno de otra clínica no se encuentra.
func (s *PostgresReportStore) Patient(
	ctx context.Context,
	db database.DBTX,
	clinicID, patientID string,
) (AgendaPatient, error) {
	var patient AgendaPatient

	err := db.QueryRow(ctx, `
		SELECT id::text, identification, first_name, last_name
		FROM patients
		WHERE clinic_id = $1 AND id = $2
	`, clinicID, patientID).Scan(
		&patient.ID,
		&patient.Identification,
		&patient.FirstName,
		&patient.LastName,
	)
	if err != nil {
		return AgendaPatient{}, database.MapError(err)
	}

	return patient, nil
}

// History devuelve las citas de un paciente, de la más reciente a la más
// antigua. El orden y el filtro coinciden con idx_appointments_patient
// (clinic_id, patient_id, starts_at DESC), así que la base lee las filas ya
// ordenadas. Los textos opcionales sin valor salen vacíos.
func (s *PostgresReportStore) History(
	ctx context.Context,
	db database.DBTX,
	clinicID, patientID string,
	limit int,
) ([]HistoryEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT
			a.id::text, a.starts_at, a.ends_at, a.status,
			COALESCE(a.cancellation_reason, ''),
			COALESCE(a.rescheduled_from_id::text, ''),
			pr.id::text, pr.display_name,
			s.id::text, s.name, s.duration_minutes
		FROM appointments a
		JOIN practitioners pr
			ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id
		JOIN services s
			ON s.clinic_id = a.clinic_id AND s.id = a.service_id
		WHERE a.clinic_id = $1 AND a.patient_id = $2
		ORDER BY a.starts_at DESC, a.id
		LIMIT $3
	`, clinicID, patientID, limit)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	items := make([]HistoryEntry, 0)

	for rows.Next() {
		var entry HistoryEntry

		err := rows.Scan(
			&entry.ID,
			&entry.StartsAt,
			&entry.EndsAt,
			&entry.Status,
			&entry.CancellationReason,
			&entry.RescheduledFromID,
			&entry.Practitioner.ID,
			&entry.Practitioner.DisplayName,
			&entry.Service.ID,
			&entry.Service.Name,
			&entry.Service.DurationMinutes,
		)
		if err != nil {
			return nil, database.MapError(err)
		}

		items = append(items, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return items, nil
}
