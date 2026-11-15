// Package agenda es el núcleo de MediCitas: catálogo, profesionales y
// pacientes y, conforme se implementen los requisitos que faltan, horarios,
// bloqueos, disponibilidad y citas. Viven juntos porque reservar debe leer y
// escribir todo eso en una sola transacción.
package agenda

import "time"

// MedicalService es un tipo de consulta que ofrece una clínica (RF3). Su
// duración fija el tamaño del espacio que ocupa una cita (I4). No se llama
// Service para no confundirlo con la capa de servicio.
type MedicalService struct {
	ID              string    `json:"id"`
	ClinicID        string    `json:"clinic_id"`
	Name            string    `json:"name"`
	DurationMinutes int       `json:"duration_minutes"`
	SpecialtyID     string    `json:"specialty_id"`
	Specialty       string    `json:"specialty"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
}

// Practitioner es un profesional de salud y los servicios que puede atender
// (RF4).
type Practitioner struct {
	ID       string `json:"id"`
	ClinicID string `json:"clinic_id"`
	// UserID apunta al usuario en Tenancy. No hay llave foránea porque vive en
	// otra base de datos; se valida al registrar al profesional.
	UserID string `json:"user_id"`
	// UserSubject es el subject de Keycloak de ese usuario. Permite reconocer
	// a un profesional a partir de su token ("solo los propios").
	UserSubject string    `json:"keycloak_subject"`
	DisplayName string    `json:"display_name"`
	SpecialtyID string    `json:"specialty_id"`
	Specialty   string    `json:"specialty"`
	Active      bool      `json:"active"`
	ServiceIDs  []string  `json:"service_ids"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewMedicalService es la entrada para crear un servicio.
type NewMedicalService struct {
	Name            string
	DurationMinutes int
	Specialty       string
}

// NewPractitioner es la entrada para registrar a un profesional.
type NewPractitioner struct {
	UserID      string
	UserSubject string
	DisplayName string
	Specialty   string
}

// StaffUser es lo que Agenda necesita saber de un usuario de Tenancy.
type StaffUser struct {
	ID       string `json:"id"`
	ClinicID string `json:"clinic_id"`
	Subject  string `json:"keycloak_subject"`
	Role     string `json:"role"`
	Active   bool   `json:"active"`
}
