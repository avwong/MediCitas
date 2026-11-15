// Package tenancy registra las clínicas y las cuentas de usuario que trabajan
// en ellas. Es el único servicio que opera sobre todas las clínicas y el único
// que guarda credenciales de la API de administración de Keycloak.
package tenancy

import (
	"time"

	"github.com/avwong/MediCitas/internal/platform/auth"
)

// Estados de una clínica. Queda en provisioning mientras corre la saga de
// creación y pasa a active cuando todos los pasos terminaron bien.
const (
	ClinicProvisioning = "provisioning"
	ClinicActive       = "active"
)

// Clinic es un tenant de la plataforma (RF1).
type Clinic struct {
	ID         string    `json:"id"`
	Identifier string    `json:"identifier"`
	Name       string    `json:"name"`
	Timezone   string    `json:"timezone"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// User relaciona una identidad de Keycloak con su clínica y su rol (RF2).
// Keycloak guarda las credenciales y es la fuente de verdad del rol; el rol de
// aquí es una copia para los listados y las restricciones de la base.
type User struct {
	ID        string    `json:"id"`
	ClinicID  string    `json:"clinic_id,omitempty"`
	Subject   string    `json:"keycloak_subject"`
	Username  string    `json:"username"`
	Role      auth.Role `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// Account es la identidad que se crea en Keycloak.
type Account struct {
	Username  string
	Email     string
	FirstName string
	LastName  string
	Password  string
}

// NewIdentity es una cuenta junto con la clínica y el rol que recibe.
type NewIdentity struct {
	Account  Account
	ClinicID string
	Role     auth.Role
}

// CreateClinicInput es la entrada de RF1.
type CreateClinicInput struct {
	Identifier string
	Name       string
	Timezone   string
	Admin      Account
}

// CreateUserInput es la entrada de RF2.
type CreateUserInput struct {
	Account Account
	Role    string
}
