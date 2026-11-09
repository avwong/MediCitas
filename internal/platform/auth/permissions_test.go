package auth

import "testing"

// TestPermissionMatrix checks the role mapping against the permission matrix
// of the assignment, row by row. A failure here means the matrix changed.
func TestPermissionMatrix(t *testing.T) {
	type row struct {
		permission Permission
		allowed    map[Role]bool
	}

	all := func(platform, clinicAdmin, reception, practitioner bool) map[Role]bool {
		return map[Role]bool{
			RolePlatformAdmin: platform,
			RoleClinicAdmin:   clinicAdmin,
			RoleReception:     reception,
			RolePractitioner:  practitioner,
			RoleService:       false,
		}
	}

	matrix := []row{
		{PermClinicsManage, all(true, false, false, false)},     // RF1
		{PermUsersManage, all(false, true, false, false)},       // RF2
		{PermCatalogManage, all(false, true, false, false)},     // RF3, RF4
		{PermScheduleManage, all(false, true, false, true)},     // RF5, RF6 (own only)
		{PermAvailabilityRead, all(false, true, true, true)},    // RF7, RF8
		{PermAppointmentsBook, all(false, true, true, false)},   // RF9 to RF11
		{PermAppointmentsOutcome, all(false, true, true, true)}, // RF12
		{PermPatientsWrite, all(false, true, true, false)},      // RF14
		{PermPatientsRead, all(false, true, true, true)},        // RF14 read only
		{PermNotesOwn, all(false, false, false, true)},          // RF15
		{PermHistoryRead, all(false, true, true, true)},         // RF16
		{PermAuditRead, all(false, true, false, false)},         // RF19
	}

	for _, r := range matrix {
		for role, want := range r.allowed {
			if got := RoleHasPermission(role, r.permission); got != want {
				t.Errorf("%s with %s = %v, want %v", role, r.permission, got, want)
			}
		}
	}
}

func TestPlatformAdminHasNoClinicData(t *testing.T) {
	clinicPermissions := []Permission{
		PermCatalogRead,
		PermAvailabilityRead,
		PermPatientsRead,
		PermNotesOwn,
		PermHistoryRead,
		PermAuditRead,
		PermAgendaRead,
		PermReportsRead,
	}

	for _, permission := range clinicPermissions {
		if RoleHasPermission(RolePlatformAdmin, permission) {
			t.Errorf("platform administrator must not have %s", permission)
		}
	}
}

func TestParseClinicRoleRejectsPrivilegedRoles(t *testing.T) {
	for _, name := range []string{"platform_admin", "service", "", "admin"} {
		if _, ok := ParseClinicRole(name); ok {
			t.Errorf("%q must not be assignable as a clinic role", name)
		}
	}

	for _, name := range []string{"clinic_admin", "reception", "practitioner"} {
		if _, ok := ParseClinicRole(name); !ok {
			t.Errorf("%q should be a clinic role", name)
		}
	}
}
