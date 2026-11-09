package platform

type Clinic struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Timezone   string `json:"timezone"`
}

type User struct {
	ID       string `json:"id"`
	ClinicID string `json:"clinic_id,omitempty"`
	Subject  string `json:"keycloak_subject"`
	Role     string `json:"role"`
	Active   bool   `json:"active"`
}

type Service struct {
	ID              string `json:"id"`
	ClinicID        string `json:"clinic_id"`
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
	Specialty       string `json:"specialty"`
	Active          bool   `json:"active"`
}

type Practitioner struct {
	ID          string   `json:"id"`
	ClinicID    string   `json:"clinic_id"`
	UserID      string   `json:"user_id"`
	DisplayName string   `json:"display_name"`
	Specialty   string   `json:"specialty"`
	ServiceIDs  []string `json:"service_ids"`
}

type CreateClinicInput struct {
	Identifier   string
	Name         string
	Timezone     string
	AdminSubject string
}

type CreateUserInput struct {
	Subject string
	Role    string
}

type CreateServiceInput struct {
	Name            string
	DurationMinutes int
	Specialty       string
}

type CreatePractitionerInput struct {
	UserID      string
	DisplayName string
	Specialty   string
	ServiceIDs  []string
}
