package auth

// Role es un rol de dominio. La única fuente de verdad son los roles de realm
// de Keycloak con estos nombres: cada servicio lee el rol del token firmado.
type Role string

const (
	RolePlatformAdmin Role = "platform_admin"
	RoleClinicAdmin   Role = "clinic_admin"
	RoleReception     Role = "reception"
	RolePractitioner  Role = "practitioner"
	// RoleService identifica las cuentas de servicio con las que los servicios
	// se llaman entre sí cuando no hay un usuario de por medio.
	RoleService Role = "service"
)

var knownRoles = map[Role]struct{}{
	RolePlatformAdmin: {},
	RoleClinicAdmin:   {},
	RoleReception:     {},
	RolePractitioner:  {},
	RoleService:       {},
}

// IsClinicRole indica si el rol pertenece siempre a exactamente una clínica.
func (r Role) IsClinicRole() bool {
	return r == RoleClinicAdmin || r == RoleReception || r == RolePractitioner
}

// ParseClinicRole devuelve el rol de clínica con ese nombre. Rechaza los roles
// de plataforma y de servicio, de modo que nunca se puedan asignar desde la
// API de la clínica.
func ParseClinicRole(value string) (Role, bool) {
	role := Role(value)
	return role, role.IsClinicRole()
}

// DomainRoleNames lista los roles de realm que puede tener un usuario humano.
// Cada usuario tiene exactamente uno.
func DomainRoleNames() []string {
	return []string{
		string(RolePlatformAdmin),
		string(RoleClinicAdmin),
		string(RoleReception),
		string(RolePractitioner),
	}
}
