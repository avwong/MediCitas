// Package tenancy registers clinics and the user accounts that work in them.
// It is the only service that operates across clinics and the only one that
// holds credentials for Keycloak's admin API.
package tenancy

import (
	"time"

	"github.com/avwong/MediCitas/internal/platform/auth"
)

// Clinic statuses. A clinic stays in provisioning while the creation saga
// runs and becomes active once every step has succeeded.
const (
	ClinicProvisioning = "provisioning"
	ClinicActive       = "active"
)

// Clinic is a tenant of the platform (RF1).
type Clinic struct {
	ID         string    `json:"id"`
	Identifier string    `json:"identifier"`
	Name       string    `json:"name"`
	Timezone   string    `json:"timezone"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// User links a Keycloak identity with its clinic and role (RF2). Keycloak
// holds the credentials and is the source of truth for the role; role here
// is a copy kept for listings and for the database constraints.
type User struct {
	ID        string    `json:"id"`
	ClinicID  string    `json:"clinic_id,omitempty"`
	Subject   string    `json:"keycloak_subject"`
	Username  string    `json:"username"`
	Role      auth.Role `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// Account is the identity to create in Keycloak.
type Account struct {
	Username  string
	Email     string
	FirstName string
	LastName  string
	Password  string
}

// NewIdentity is an account together with the clinic and role it gets.
type NewIdentity struct {
	Account  Account
	ClinicID string
	Role     auth.Role
}

// CreateClinicInput is the input of RF1.
type CreateClinicInput struct {
	Identifier string
	Name       string
	Timezone   string
	Admin      Account
}

// CreateUserInput is the input of RF2.
type CreateUserInput struct {
	Account Account
	Role    string
}
