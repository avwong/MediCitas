package auth

// Role is a domain role. Keycloak realm roles with these names are the single
// source of truth: every service reads the role from the signed token.
type Role string

const (
	RolePlatformAdmin Role = "platform_admin"
	RoleClinicAdmin   Role = "clinic_admin"
	RoleReception     Role = "reception"
	RolePractitioner  Role = "practitioner"
	// RoleService identifies the service accounts that services use to call
	// each other when no user is involved.
	RoleService Role = "service"
)

var knownRoles = map[Role]struct{}{
	RolePlatformAdmin: {},
	RoleClinicAdmin:   {},
	RoleReception:     {},
	RolePractitioner:  {},
	RoleService:       {},
}

// IsClinicRole reports whether the role always belongs to exactly one clinic.
func (r Role) IsClinicRole() bool {
	return r == RoleClinicAdmin || r == RoleReception || r == RolePractitioner
}

// ParseClinicRole returns the clinic role named value. Platform and service
// roles are rejected, so they can never be assigned through the clinic API.
func ParseClinicRole(value string) (Role, bool) {
	role := Role(value)
	return role, role.IsClinicRole()
}

// DomainRoleNames lists the realm roles a human user may hold. Exactly one of
// them is assigned to each user.
func DomainRoleNames() []string {
	return []string{
		string(RolePlatformAdmin),
		string(RoleClinicAdmin),
		string(RoleReception),
		string(RolePractitioner),
	}
}
