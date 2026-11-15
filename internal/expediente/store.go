package expediente

import (
	"context"

	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

// NoteStore es la persistencia que usa el servicio de RF15.
type NoteStore interface {
	Insert(
		ctx context.Context,
		db database.DBTX,
		in NewClinicalNote,
	) (ClinicalNote, error)

	GetOwned(
		ctx context.Context,
		db database.DBTX,
		clinicID string,
		noteID string,
		practitionerSubject string,
	) (ClinicalNote, error)
}

// PostgresStore guarda las notas clínicas en expediente_db.
//
// No tiene un pool propio: toda operación de RF15 pasa por TxManager, para que
// la nota y su evento de auditoría de RF18 compartan una transacción.
type PostgresStore struct{}

// NewPostgresStore devuelve el almacén de RF15 sobre PostgreSQL.
func NewPostgresStore() *PostgresStore {
	return &PostgresStore{}
}

const clinicalNoteColumns = `
    id::text,
    clinic_id::text,
    appointment_id::text,
    patient_id::text,
    practitioner_subject,
    content,
    created_at
`

func scanClinicalNote(row pgx.Row) (ClinicalNote, error) {
	var note ClinicalNote

	err := row.Scan(
		&note.ID,
		&note.ClinicID,
		&note.AppointmentID,
		&note.PatientID,
		&note.PractitionerSubject,
		&note.Content,
		&note.CreatedAt,
	)

	return note, err
}

// Insert crea una nota. La restricción única sobre clinic_id y appointment_id
// impide que una misma cita tenga dos notas.
func (s *PostgresStore) Insert(
	ctx context.Context,
	db database.DBTX,
	in NewClinicalNote,
) (ClinicalNote, error) {
	row := db.QueryRow(ctx, `
        INSERT INTO clinical_notes (
            clinic_id,
            appointment_id,
            patient_id,
            practitioner_subject,
            content
        )
        VALUES ($1,$2,$3,$4,$5)
        RETURNING `+clinicalNoteColumns,
		in.ClinicID,
		in.AppointmentID,
		in.PatientID,
		in.PractitionerSubject,
		in.Content,
	)

	note, err := scanClinicalNote(row)
	if err != nil {
		return ClinicalNote{}, database.MapError(err)
	}

	return note, nil
}

// GetOwned devuelve una nota solo si pertenece a la clínica y al profesional
// autenticados. Una nota de otra clínica o de otro profesional no se distingue
// de una que no existe.
func (s *PostgresStore) GetOwned(
	ctx context.Context,
	db database.DBTX,
	clinicID string,
	noteID string,
	practitionerSubject string,
) (ClinicalNote, error) {
	row := db.QueryRow(ctx, `
        SELECT `+clinicalNoteColumns+`
        FROM clinical_notes
        WHERE clinic_id = $1
          AND id = $2
          AND practitioner_subject = $3
    `,
		clinicID,
		noteID,
		practitionerSubject,
	)

	note, err := scanClinicalNote(row)
	if err != nil {
		return ClinicalNote{}, database.MapError(err)
	}

	return note, nil
}
