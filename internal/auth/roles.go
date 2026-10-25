package auth

type Permission string

const (
	PermissionPlatformManage Permission = "platform:manage"
	PermissionClinicManage   Permission = "clinic:manage"
	PermissionAppointments   Permission = "appointments:manage"
	PermissionOwnAgenda      Permission = "agenda:own"
)

var rolePermissions = map[string]map[Permission]struct{}{
	"platform_admin": {
		PermissionPlatformManage: {},
	},

	"clinic_admin": {
		PermissionClinicManage: {},
		PermissionAppointments: {},
	},

	"reception": {
		PermissionAppointments: {},
	},

	"practitioner": {
		PermissionOwnAgenda: {},
	},
}

func HasPermission(claims *Claims, permission Permission) bool {
	for _, role := range claims.RealmAccess.Roles {
		permissions, exists := rolePermissions[role]
		if !exists {
			continue
		}

		if _, allowed := permissions[permission]; allowed {
			return true
		}
	}

	return false
}
