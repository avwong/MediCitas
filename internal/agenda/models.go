// Package agenda is the core of MediCitas: catalog, practitioners and, as the
// remaining requirements land, schedules, blocks, patients, availability and
// appointments. They live together because booking must read and write all
// of them in a single transaction.
package agenda

import "time"

// MedicalService is a type of consultation offered by a clinic (RF3). Its
// duration fixes the size of the slot an appointment takes (I4). It is not
// called Service to avoid confusing it with the service layer.
type MedicalService struct {
	ID              string    `json:"id"`
	ClinicID        string    `json:"clinic_id"`
	Name            string    `json:"name"`
	DurationMinutes int       `json:"duration_minutes"`
	Specialty       string    `json:"specialty"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`
}

// Practitioner is a health professional and the services they may attend
// (RF4).
type Practitioner struct {
	ID       string `json:"id"`
	ClinicID string `json:"clinic_id"`
	// UserID references the user in the tenancy service. There is no foreign
	// key because it lives in another database; it is validated when the
	// practitioner is registered.
	UserID string `json:"user_id"`
	// UserSubject is the Keycloak subject of that user. It lets the service
	// recognize a practitioner from their token ("only their own").
	UserSubject string    `json:"keycloak_subject"`
	DisplayName string    `json:"display_name"`
	Specialty   string    `json:"specialty"`
	Active      bool      `json:"active"`
	ServiceIDs  []string  `json:"service_ids"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewMedicalService is the input to create a service.
type NewMedicalService struct {
	Name            string
	DurationMinutes int
	Specialty       string
}

// NewPractitioner is the input to register a practitioner.
type NewPractitioner struct {
	UserID      string
	UserSubject string
	DisplayName string
	Specialty   string
}

// StaffUser is the view of a tenancy user that the agenda needs.
type StaffUser struct {
	ID       string `json:"id"`
	ClinicID string `json:"clinic_id"`
	Subject  string `json:"keycloak_subject"`
	Role     string `json:"role"`
	Active   bool   `json:"active"`
}
