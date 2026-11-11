package expediente

import (
	"context"

	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

// NoteStore is the persistence boundary used by the RF15 service.
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

// PostgresStore persists clinical notes in expediente_db.
//
// It does not own a pool: every RF15 operation is run through TxManager so
// the note operation and its RF18 audit event share one transaction.
type PostgresStore struct{}

// NewPostgresStore returns the PostgreSQL RF15 store.
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

// Insert creates one note. The unique constraint on clinic_id +
// appointment_id prevents two notes from being attached to the same
// appointment.
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

// GetOwned returns a note only when it belongs to the authenticated clinic
// and practitioner. Another clinic or practitioner is indistinguishable from
// a nonexistent note.
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
