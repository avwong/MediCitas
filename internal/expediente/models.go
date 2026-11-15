// Package expediente guarda las notas clínicas (RF15). Vive aparte del resto
// del dominio, con su propia base y sus propias credenciales, porque es el
// dato más sensible del sistema.
package expediente

import "time"

const (
	// AppointmentStatusAttended es el estado que Agenda debe reportar para que
	// Expediente acepte una nota clínica.
	AppointmentStatusAttended = "attended"
)

// AppointmentContext es la información mínima que Expediente necesita de
// Agenda para decidir si RF15 puede crear una nota clínica.
//
// Agenda sigue siendo la fuente de verdad de las citas. Este tipo es solo el
// contrato entre los dos servicios.
type AppointmentContext struct {
	AppointmentID       string `json:"appointment_id"`
	ClinicID            string `json:"clinic_id"`
	PatientID           string `json:"patient_id"`
	PractitionerSubject string `json:"practitioner_subject"`
	Status              string `json:"status"`
}

// ClinicalNote es el registro sensible de RF15, que solo se guarda en
// expediente_db.
type ClinicalNote struct {
	ID                  string    `json:"id"`
	ClinicID            string    `json:"clinic_id"`
	AppointmentID       string    `json:"appointment_id"`
	PatientID           string    `json:"patient_id"`
	PractitionerSubject string    `json:"practitioner_subject"`
	Content             string    `json:"content"`
	CreatedAt           time.Time `json:"created_at"`
}

// CreateNoteInput contiene los únicos valores que puede elegir quien llama
// desde fuera.
//
// La clínica, el paciente y el profesional salen de fuentes confiables y no de
// los datos de la petición.
type CreateNoteInput struct {
	AppointmentID string
	Content       string
}

// NewClinicalNote es el registro ya autorizado que se entrega a la
// persistencia.
type NewClinicalNote struct {
	ClinicID            string
	AppointmentID       string
	PatientID           string
	PractitionerSubject string
	Content             string
}
