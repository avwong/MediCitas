package agenda

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/database"
)

// practitionerRef es lo que la agenda necesita saber de un profesional.
type practitionerRef struct {
	ID          string
	DisplayName string
	UserSubject string
	Active      bool
}

// serviceRef es lo que la agenda necesita saber de un servicio.
type serviceRef struct {
	ID       string
	Duration time.Duration
	Active   bool
}

// newAppointment son los datos de una cita por insertar. No lleva ends_at: lo
// calcula la base (I4).
type newAppointment struct {
	PatientID         string
	PractitionerID    string
	ServiceID         string
	StartsAt          time.Time
	CreatedBy         string
	RescheduledFromID *string
}

// noteContext es lo que Expediente necesita de una cita para RF15.
type noteContext struct {
	AppointmentID       string `json:"appointment_id"`
	ClinicID            string `json:"clinic_id"`
	PatientID           string `json:"patient_id"`
	PractitionerSubject string `json:"practitioner_subject"`
	Status              string `json:"status"`
}

// SchedulingStore guarda horarios, bloqueos y citas. Igual que PatientStore,
// cada método recibe la conexión o transacción en la que debe correr y solo ve
// filas de la clínica recibida (I5).
type SchedulingStore interface {
	ClinicTimezone(ctx context.Context, db database.DBTX, clinicID string) (string, error)
	Practitioner(ctx context.Context, db database.DBTX, clinicID, practitionerID string, lock bool) (practitionerRef, error)
	Service(ctx context.Context, db database.DBTX, clinicID, serviceID string) (serviceRef, error)
	PractitionerOffers(ctx context.Context, db database.DBTX, clinicID, practitionerID, serviceID string) (bool, error)
	PractitionersForService(ctx context.Context, db database.DBTX, clinicID, serviceID string) ([]practitionerRef, error)
	PatientActive(ctx context.Context, db database.DBTX, clinicID, patientID string) (bool, error)

	Schedule(ctx context.Context, db database.DBTX, clinicID, practitionerID string) ([]ScheduleBlock, error)
	ReplaceSchedule(ctx context.Context, db database.DBTX, clinicID, practitionerID string, blocks []ScheduleBlock) error

	InsertBlock(ctx context.Context, db database.DBTX, block AgendaBlock) (AgendaBlock, error)
	Block(ctx context.Context, db database.DBTX, clinicID, practitionerID, blockID string) (AgendaBlock, error)
	DeleteBlock(ctx context.Context, db database.DBTX, clinicID, practitionerID, blockID string) error
	BlocksInRange(ctx context.Context, db database.DBTX, clinicID, practitionerID string, from, to time.Time) ([]AgendaBlock, error)

	InsertAppointment(ctx context.Context, db database.DBTX, clinicID string, in newAppointment) (Appointment, error)
	Appointment(ctx context.Context, db database.DBTX, clinicID, appointmentID string, lock bool) (Appointment, error)
	CloseAppointment(ctx context.Context, db database.DBTX, clinicID, appointmentID, status, closedBy string, reason *string, onlyIfStarted bool) (Appointment, bool, error)
	BookedInRange(ctx context.Context, db database.DBTX, clinicID, practitionerID string, from, to time.Time) ([]Appointment, error)
	NoteContext(ctx context.Context, db database.DBTX, clinicID, appointmentID string) (noteContext, error)
}

// PostgresSchedulingStore implementa SchedulingStore sobre agenda_db.
type PostgresSchedulingStore struct{}

// NewPostgresSchedulingStore devuelve el almacén de horarios, bloqueos y
// citas.
func NewPostgresSchedulingStore() *PostgresSchedulingStore {
	return &PostgresSchedulingStore{}
}

var _ SchedulingStore = (*PostgresSchedulingStore)(nil)

// ClinicTimezone devuelve la zona horaria de la clínica.
func (s *PostgresSchedulingStore) ClinicTimezone(ctx context.Context, db database.DBTX, clinicID string) (string, error) {
	var timezone string

	err := db.QueryRow(ctx, `
		SELECT timezone
		FROM clinics
		WHERE id = $1
	`, clinicID).Scan(&timezone)

	return timezone, database.MapError(err)
}

// Practitioner devuelve un profesional de la clínica. Con lock = true bloquea
// su fila (FOR UPDATE) hasta el final de la transacción: es el candado que
// comparten reservar, reprogramar, cambiar el horario y crear bloqueos, para
// que ninguno de esos cambios se cuele entre la validación de una reserva y
// su inserción (I3).
func (s *PostgresSchedulingStore) Practitioner(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID string,
	lock bool,
) (practitionerRef, error) {
	query := `
		SELECT id::text, display_name, user_subject, active
		FROM practitioners
		WHERE clinic_id = $1
		  AND id = $2
	`
	if lock {
		query += " FOR UPDATE"
	}

	var ref practitionerRef

	err := db.QueryRow(ctx, query, clinicID, practitionerID).Scan(
		&ref.ID,
		&ref.DisplayName,
		&ref.UserSubject,
		&ref.Active,
	)

	return ref, database.MapError(err)
}

// Service devuelve la duración y el estado de un servicio de la clínica.
func (s *PostgresSchedulingStore) Service(ctx context.Context, db database.DBTX, clinicID, serviceID string) (serviceRef, error) {
	var (
		ref     serviceRef
		minutes int
	)

	err := db.QueryRow(ctx, `
		SELECT id::text, duration_minutes, active
		FROM services
		WHERE clinic_id = $1
		  AND id = $2
	`, clinicID, serviceID).Scan(&ref.ID, &minutes, &ref.Active)
	if err != nil {
		return serviceRef{}, database.MapError(err)
	}

	ref.Duration = time.Duration(minutes) * time.Minute

	return ref, nil
}

// PractitionerOffers indica si el profesional tiene asociado el servicio
// (RF4).
func (s *PostgresSchedulingStore) PractitionerOffers(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID, serviceID string,
) (bool, error) {
	var offers bool

	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM practitioner_services
			WHERE clinic_id = $1
			  AND practitioner_id = $2
			  AND service_id = $3
		)
	`, clinicID, practitionerID, serviceID).Scan(&offers)

	return offers, database.MapError(err)
}

// PractitionersForService devuelve los profesionales activos que atienden el
// servicio, ordenados por nombre (RF8).
func (s *PostgresSchedulingStore) PractitionersForService(
	ctx context.Context,
	db database.DBTX,
	clinicID, serviceID string,
) ([]practitionerRef, error) {
	rows, err := db.Query(ctx, `
		SELECT p.id::text, p.display_name, p.user_subject, p.active
		FROM practitioner_services ps
		JOIN practitioners p
		  ON p.clinic_id = ps.clinic_id
		 AND p.id = ps.practitioner_id
		WHERE ps.clinic_id = $1
		  AND ps.service_id = $2
		  AND p.active
		ORDER BY p.display_name, p.id
	`, clinicID, serviceID)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	refs := []practitionerRef{}

	for rows.Next() {
		var ref practitionerRef
		if err := rows.Scan(&ref.ID, &ref.DisplayName, &ref.UserSubject, &ref.Active); err != nil {
			return nil, err
		}

		refs = append(refs, ref)
	}

	return refs, rows.Err()
}

// PatientActive indica si el paciente está activo. Un paciente que no existe
// en la clínica es un error "no encontrado".
func (s *PostgresSchedulingStore) PatientActive(ctx context.Context, db database.DBTX, clinicID, patientID string) (bool, error) {
	var active bool

	err := db.QueryRow(ctx, `
		SELECT active
		FROM patients
		WHERE clinic_id = $1
		  AND id = $2
	`, clinicID, patientID).Scan(&active)

	return active, database.MapError(err)
}

// Schedule devuelve el horario semanal del profesional, ordenado por día y
// hora. to_char devuelve 24:00 para un bloque que termina a la medianoche.
func (s *PostgresSchedulingStore) Schedule(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID string,
) ([]ScheduleBlock, error) {
	rows, err := db.Query(ctx, `
		SELECT id::text,
		       weekday,
		       to_char(start_time, 'HH24:MI'),
		       to_char(end_time, 'HH24:MI')
		FROM schedules
		WHERE clinic_id = $1
		  AND practitioner_id = $2
		ORDER BY weekday, start_time
	`, clinicID, practitionerID)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	blocks := []ScheduleBlock{}

	for rows.Next() {
		var block ScheduleBlock
		if err := rows.Scan(&block.ID, &block.Weekday, &block.StartTime, &block.EndTime); err != nil {
			return nil, err
		}

		blocks = append(blocks, block)
	}

	return blocks, rows.Err()
}

// ReplaceSchedule sustituye el horario completo del profesional. Debe correr
// dentro de una transacción que ya tenga bloqueada la fila del profesional.
// Dos bloques que se solapan en el mismo día chocan con la restricción
// schedules_no_overlap.
func (s *PostgresSchedulingStore) ReplaceSchedule(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID string,
	blocks []ScheduleBlock,
) error {
	if _, err := db.Exec(ctx, `
		DELETE FROM schedules
		WHERE clinic_id = $1
		  AND practitioner_id = $2
	`, clinicID, practitionerID); err != nil {
		return database.MapError(err)
	}

	for _, block := range blocks {
		_, err := db.Exec(ctx, `
			INSERT INTO schedules (clinic_id, practitioner_id, weekday, start_time, end_time)
			VALUES ($1, $2, $3, $4::time, $5::time)
		`, clinicID, practitionerID, block.Weekday, block.StartTime, block.EndTime)
		if err != nil {
			if constraintOf(err) == "schedules_no_overlap" {
				return apperr.Invalid("schedule blocks of the same day must not overlap")
			}

			return database.MapError(err)
		}
	}

	return nil
}

const blockColumns = `
	id::text, clinic_id::text, practitioner_id::text,
	starts_at, ends_at, reason, created_by, created_at
`

func scanBlock(row pgx.Row) (AgendaBlock, error) {
	var block AgendaBlock

	err := row.Scan(
		&block.ID,
		&block.ClinicID,
		&block.PractitionerID,
		&block.StartsAt,
		&block.EndsAt,
		&block.Reason,
		&block.CreatedBy,
		&block.CreatedAt,
	)

	return block, err
}

// InsertBlock registra un bloqueo de agenda.
func (s *PostgresSchedulingStore) InsertBlock(ctx context.Context, db database.DBTX, block AgendaBlock) (AgendaBlock, error) {
	created, err := scanBlock(db.QueryRow(ctx, `
		INSERT INTO blocks (clinic_id, practitioner_id, starts_at, ends_at, reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+blockColumns,
		block.ClinicID,
		block.PractitionerID,
		block.StartsAt,
		block.EndsAt,
		block.Reason,
		block.CreatedBy,
	))

	return created, database.MapError(err)
}

// Block devuelve un bloqueo del profesional.
func (s *PostgresSchedulingStore) Block(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID, blockID string,
) (AgendaBlock, error) {
	block, err := scanBlock(db.QueryRow(ctx, `
		SELECT `+blockColumns+`
		FROM blocks
		WHERE clinic_id = $1
		  AND practitioner_id = $2
		  AND id = $3
	`, clinicID, practitionerID, blockID))

	return block, database.MapError(err)
}

// DeleteBlock elimina un bloqueo del profesional.
func (s *PostgresSchedulingStore) DeleteBlock(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID, blockID string,
) error {
	tag, err := db.Exec(ctx, `
		DELETE FROM blocks
		WHERE clinic_id = $1
		  AND practitioner_id = $2
		  AND id = $3
	`, clinicID, practitionerID, blockID)
	if err != nil {
		return database.MapError(err)
	}

	if tag.RowsAffected() == 0 {
		return apperr.NotFound("block not found")
	}

	return nil
}

// BlocksInRange devuelve los bloqueos del profesional que tocan [from, to).
// La condición sobre tstzrange tiene la forma del índice GiST
// idx_blocks_practitioner_period.
func (s *PostgresSchedulingStore) BlocksInRange(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID string,
	from, to time.Time,
) ([]AgendaBlock, error) {
	rows, err := db.Query(ctx, `
		SELECT `+blockColumns+`
		FROM blocks
		WHERE clinic_id = $1
		  AND practitioner_id = $2
		  AND tstzrange(starts_at, ends_at) && tstzrange($3, $4)
		ORDER BY starts_at, id
	`, clinicID, practitionerID, from, to)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	blocks := []AgendaBlock{}

	for rows.Next() {
		block, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}

		blocks = append(blocks, block)
	}

	return blocks, rows.Err()
}

const appointmentColumns = `
	id::text, clinic_id::text, patient_id::text, practitioner_id::text,
	service_id::text, starts_at, ends_at, status, rescheduled_from_id::text,
	created_by, created_at, closed_by, closed_at, cancellation_reason
`

func scanAppointment(row pgx.Row) (Appointment, error) {
	var appointment Appointment

	err := row.Scan(
		&appointment.ID,
		&appointment.ClinicID,
		&appointment.PatientID,
		&appointment.PractitionerID,
		&appointment.ServiceID,
		&appointment.StartsAt,
		&appointment.EndsAt,
		&appointment.Status,
		&appointment.RescheduledFromID,
		&appointment.CreatedBy,
		&appointment.CreatedAt,
		&appointment.ClosedBy,
		&appointment.ClosedAt,
		&appointment.CancellationReason,
	)

	return appointment, err
}

// InsertAppointment reserva el espacio. Aquí se decide la carrera entre dos
// reservas simultáneas: las restricciones de exclusión de la tabla dejan
// pasar una sola cita vigente por profesional (I1) y por paciente (I2) en un
// mismo intervalo, y la otra recibe un conflicto.
//
// ends_at se envía igual a starts_at solo porque la columna es NOT NULL; un
// trigger lo sustituye por el inicio más la duración del servicio (I4).
func (s *PostgresSchedulingStore) InsertAppointment(
	ctx context.Context,
	db database.DBTX,
	clinicID string,
	in newAppointment,
) (Appointment, error) {
	appointment, err := scanAppointment(db.QueryRow(ctx, `
		INSERT INTO appointments (
			clinic_id, patient_id, practitioner_id, service_id,
			starts_at, ends_at, created_by, rescheduled_from_id
		)
		VALUES ($1, $2, $3, $4, $5, $5, $6, $7)
		RETURNING `+appointmentColumns,
		clinicID,
		in.PatientID,
		in.PractitionerID,
		in.ServiceID,
		in.StartsAt,
		in.CreatedBy,
		in.RescheduledFromID,
	))
	if err != nil {
		switch constraintOf(err) {
		case "appointments_practitioner_no_overlap":
			return Appointment{}, apperr.Conflict("slot already taken")
		case "appointments_patient_no_overlap":
			return Appointment{}, apperr.Conflict("the patient already has an appointment at that time")
		}

		return Appointment{}, database.MapError(err)
	}

	return appointment, nil
}

// Appointment devuelve una cita de la clínica. Con lock = true bloquea la fila
// hasta el final de la transacción.
func (s *PostgresSchedulingStore) Appointment(
	ctx context.Context,
	db database.DBTX,
	clinicID, appointmentID string,
	lock bool,
) (Appointment, error) {
	query := `
		SELECT ` + appointmentColumns + `
		FROM appointments
		WHERE clinic_id = $1
		  AND id = $2
	`
	if lock {
		query += " FOR UPDATE"
	}

	appointment, err := scanAppointment(db.QueryRow(ctx, query, clinicID, appointmentID))

	return appointment, database.MapError(err)
}

// CloseAppointment saca una cita de booked hacia un estado final. La condición
// status = 'booked' va en el propio UPDATE: si dos peticiones intentan cerrar
// la misma cita, la segunda espera a la primera y después no encuentra fila.
// Con onlyIfStarted, además, la cita debe haber empezado según el reloj de la
// base (RF12). El segundo valor indica si la cita se cerró.
func (s *PostgresSchedulingStore) CloseAppointment(
	ctx context.Context,
	db database.DBTX,
	clinicID, appointmentID, status, closedBy string,
	reason *string,
	onlyIfStarted bool,
) (Appointment, bool, error) {
	appointment, err := scanAppointment(db.QueryRow(ctx, `
		UPDATE appointments
		SET status = $3,
		    closed_by = $4,
		    closed_at = now(),
		    cancellation_reason = $5
		WHERE clinic_id = $1
		  AND id = $2
		  AND status = 'booked'
		  AND (NOT $6 OR starts_at <= now())
		RETURNING `+appointmentColumns,
		clinicID,
		appointmentID,
		status,
		closedBy,
		reason,
		onlyIfStarted,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Appointment{}, false, nil
	}

	if err != nil {
		return Appointment{}, false, database.MapError(err)
	}

	return appointment, true, nil
}

// BookedInRange devuelve las citas vigentes del profesional que tocan
// [from, to). Es la consulta de la disponibilidad (RF7): su condición tiene
// la forma exacta del índice parcial GiST que crea la restricción
// appointments_practitioner_no_overlap, así que la sirve ese mismo índice.
func (s *PostgresSchedulingStore) BookedInRange(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID string,
	from, to time.Time,
) ([]Appointment, error) {
	rows, err := db.Query(ctx, `
		SELECT `+appointmentColumns+`
		FROM appointments
		WHERE clinic_id = $1
		  AND practitioner_id = $2
		  AND status = 'booked'
		  AND tstzrange(starts_at, ends_at) && tstzrange($3, $4)
		ORDER BY starts_at, id
	`, clinicID, practitionerID, from, to)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	appointments := []Appointment{}

	for rows.Next() {
		appointment, err := scanAppointment(rows)
		if err != nil {
			return nil, err
		}

		appointments = append(appointments, appointment)
	}

	return appointments, rows.Err()
}

// NoteContext devuelve lo que Expediente necesita de una cita para aceptar
// una nota clínica (RF15).
func (s *PostgresSchedulingStore) NoteContext(
	ctx context.Context,
	db database.DBTX,
	clinicID, appointmentID string,
) (noteContext, error) {
	var result noteContext

	err := db.QueryRow(ctx, `
		SELECT a.id::text, a.clinic_id::text, a.patient_id::text, p.user_subject, a.status
		FROM appointments a
		JOIN practitioners p
		  ON p.clinic_id = a.clinic_id
		 AND p.id = a.practitioner_id
		WHERE a.clinic_id = $1
		  AND a.id = $2
	`, clinicID, appointmentID).Scan(
		&result.AppointmentID,
		&result.ClinicID,
		&result.PatientID,
		&result.PractitionerSubject,
		&result.Status,
	)

	return result, database.MapError(err)
}

// constraintOf devuelve el nombre de la restricción que violó err, o "".
func constraintOf(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}

	return ""
}
