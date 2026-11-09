package auth

// Permission is a domain permission. Each one corresponds to a row of the
// permission matrix in the assignment, so the mapping below can be checked
// against it line by line.
type Permission string

const (
	// RF1: create clinics.
	PermClinicsManage Permission = "clinics:manage"
	// RF2: manage the users of a clinic.
	PermUsersManage Permission = "users:manage"
	// RF3, RF4: manage services and practitioners.
	PermCatalogManage Permission = "catalog:manage"
	// Documented extension of the matrix: read the catalog. Reception and
	// practitioners need it to book and to consult availability.
	PermCatalogRead Permission = "catalog:read"
	// RF5, RF6: schedules and blocks. Practitioners only manage their own;
	// that ownership rule is enforced inside the service.
	PermScheduleManage Permission = "schedule:manage"
	// RF7, RF8: availability.
	PermAvailabilityRead Permission = "availability:read"
	// RF9 to RF11: book, reschedule and cancel.
	PermAppointmentsBook Permission = "appointments:book"
	// RF12: register the outcome.
	PermAppointmentsOutcome Permission = "appointments:outcome"
	// RF14: create and update patients.
	PermPatientsWrite Permission = "patients:write"
	// RF14: read patients (practitioners have read-only access).
	PermPatientsRead Permission = "patients:read"
	// RF15: clinical notes, only for the practitioner's own appointments
	// (ownership enforced inside the service).
	PermNotesOwn Permission = "notes:own"
	// RF16: patient history.
	PermHistoryRead Permission = "history:read"
	// RF19: audit log.
	PermAuditRead Permission = "audit:read"
	// RF20 (not in the matrix; documented): daily agenda.
	PermAgendaRead Permission = "agenda:read"
	// RF21 (not in the matrix; documented): appointment counts by status.
	PermReportsRead Permission = "reports:read"
)

var rolePermissions = map[Role]map[Permission]struct{}{
	RolePlatformAdmin: set(
		PermClinicsManage,
	),
	RoleClinicAdmin: set(
		PermUsersManage,
		PermCatalogManage,
		PermCatalogRead,
		PermScheduleManage,
		PermAvailabilityRead,
		PermAppointmentsBook,
		PermAppointmentsOutcome,
		PermPatientsWrite,
		PermPatientsRead,
		PermHistoryRead,
		PermAuditRead,
		PermAgendaRead,
		PermReportsRead,
	),
	RoleReception: set(
		PermCatalogRead,
		PermAvailabilityRead,
		PermAppointmentsBook,
		PermAppointmentsOutcome,
		PermPatientsWrite,
		PermPatientsRead,
		PermHistoryRead,
		PermAgendaRead,
	),
	RolePractitioner: set(
		PermCatalogRead,
		PermScheduleManage,
		PermAvailabilityRead,
		PermAppointmentsOutcome,
		PermPatientsRead,
		PermNotesOwn,
		PermHistoryRead,
		PermAgendaRead,
	),
}

func set(permissions ...Permission) map[Permission]struct{} {
	result := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		result[permission] = struct{}{}
	}

	return result
}

// RoleHasPermission reports whether role grants permission.
func RoleHasPermission(role Role, permission Permission) bool {
	_, ok := rolePermissions[role][permission]
	return ok
}
