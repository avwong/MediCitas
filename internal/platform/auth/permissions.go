package auth

// Permission es un permiso de dominio. Cada uno corresponde a una fila de la
// matriz de permisos del enunciado, así que el mapa de abajo se puede revisar
// contra ella línea por línea.
type Permission string

const (
	// RF1: crear clínicas.
	PermClinicsManage Permission = "clinics:manage"
	// RF2: administrar los usuarios de una clínica.
	PermUsersManage Permission = "users:manage"
	// RF3 y RF4: administrar servicios y profesionales.
	PermCatalogManage Permission = "catalog:manage"
	// Extensión documentada de la matriz: leer el catálogo. Recepción y los
	// profesionales lo necesitan para reservar y consultar la disponibilidad.
	PermCatalogRead Permission = "catalog:read"
	// RF5 y RF6: horarios y bloqueos. El profesional solo administra los
	// suyos; esa regla se aplica dentro del servicio.
	PermScheduleManage Permission = "schedule:manage"
	// RF7 y RF8: disponibilidad.
	PermAvailabilityRead Permission = "availability:read"
	// RF9 a RF11: reservar, reprogramar y cancelar.
	PermAppointmentsBook Permission = "appointments:book"
	// RF12: registrar el resultado de la cita.
	PermAppointmentsOutcome Permission = "appointments:outcome"
	// RF14: crear y actualizar pacientes.
	PermPatientsWrite Permission = "patients:write"
	// RF14: leer pacientes (el profesional solo tiene lectura).
	PermPatientsRead Permission = "patients:read"
	// RF15: notas clínicas, solo de las citas propias del profesional (la
	// pertenencia se comprueba dentro del servicio).
	PermNotesOwn Permission = "notes:own"
	// RF16: historial del paciente.
	PermHistoryRead Permission = "history:read"
	// RF19: bitácora de auditoría.
	PermAuditRead Permission = "audit:read"
	// RF20 (no está en la matriz; documentado): agenda del día.
	PermAgendaRead Permission = "agenda:read"
	// RF21 (no está en la matriz; documentado): conteo de citas por estado.
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

// RoleHasPermission indica si el rol concede el permiso.
func RoleHasPermission(role Role, permission Permission) bool {
	_, ok := rolePermissions[role][permission]
	return ok
}
