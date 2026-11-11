package expediente

import "time"

const (
	// AppointmentStatusAttended is the status Agenda must expose before
	// Expediente accepts a clinical note.
	AppointmentStatusAttended = "attended"
)

// AppointmentContext is the minimal information Expediente needs from Agenda
// to decide whether RF15 may create a clinical note.
//
// Agenda remains the source of truth for appointments. This type is only the
// service-to-service contract.
type AppointmentContext struct {
	AppointmentID       string `json:"appointment_id"`
	ClinicID            string `json:"clinic_id"`
	PatientID           string `json:"patient_id"`
	PractitionerSubject string `json:"practitioner_subject"`
	Status              string `json:"status"`
}

// ClinicalNote is the sensitive RF15 record stored only in expediente_db.
type ClinicalNote struct {
	ID                  string    `json:"id"`
	ClinicID            string    `json:"clinic_id"`
	AppointmentID       string    `json:"appointment_id"`
	PatientID           string    `json:"patient_id"`
	PractitionerSubject string    `json:"practitioner_subject"`
	Content             string    `json:"content"`
	CreatedAt           time.Time `json:"created_at"`
}

// CreateNoteInput contains the only values the external caller may choose.
//
// Clinic, patient and practitioner are derived from trusted sources instead of
// request data.
type CreateNoteInput struct {
	AppointmentID string
	Content       string
}

// NewClinicalNote is the already-authorized record passed to persistence.
type NewClinicalNote struct {
	ClinicID            string
	AppointmentID       string
	PatientID           string
	PractitionerSubject string
	Content             string
}
