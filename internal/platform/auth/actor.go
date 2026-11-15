package auth

import (
	"errors"
	"strings"

	"github.com/avwong/MediCitas/internal/platform/ids"
)

var (
	// ErrNoSubject indica que el token no trae subject.
	ErrNoSubject = errors.New("token has no subject")
	// ErrNoDomainRole indica que el token no trae ningún rol de MediCitas.
	ErrNoDomainRole = errors.New("token carries no domain role")
	// ErrAmbiguousRole indica que el token trae más de un rol de MediCitas.
	ErrAmbiguousRole = errors.New("token carries more than one domain role")
	// ErrNoClinic indica que un rol de clínica llegó sin un claim de clínica
	// válido.
	ErrNoClinic = errors.New("clinic role without a valid clinic claim")
)

// Claims son los claims del token de acceso que usa MediCitas.
type Claims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	AuthorizedParty   string `json:"azp"`
	// ClinicID lo escribe Tenancy como atributo del usuario en Keycloak y un
	// protocol mapper lo copia al token. Como viaja firmado, ningún servicio
	// toma la clínica de la petición.
	ClinicID    string `json:"clinic_id"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Actor es quien hace la petición, ya autenticado.
type Actor struct {
	Subject  string
	Username string
	// ClinicID queda vacío para la administración de plataforma y las cuentas
	// de servicio.
	ClinicID string
	Role     Role
	// ClientID es el client de Keycloak que obtuvo el token (azp).
	ClientID string
	// Token es el bearer token original. Se conserva para reenviar la
	// identidad del usuario cuando un servicio llama a otro en su nombre.
	Token string
}

// NewActor valida los claims y construye el actor. El token debe traer
// exactamente un rol de dominio, y los roles de clínica deben traer una
// clínica.
func NewActor(claims Claims, rawToken string) (Actor, error) {
	if strings.TrimSpace(claims.Subject) == "" {
		return Actor{}, ErrNoSubject
	}

	var roles []Role
	for _, name := range claims.RealmAccess.Roles {
		role := Role(name)
		if _, known := knownRoles[role]; known {
			roles = append(roles, role)
		}
	}

	switch len(roles) {
	case 0:
		return Actor{}, ErrNoDomainRole
	case 1:
	default:
		return Actor{}, ErrAmbiguousRole
	}

	role := roles[0]
	clinicID := ""

	if role.IsClinicRole() {
		normalized, ok := ids.Normalize(claims.ClinicID)
		if !ok {
			return Actor{}, ErrNoClinic
		}

		clinicID = normalized
	}

	return Actor{
		Subject:  claims.Subject,
		Username: claims.PreferredUsername,
		ClinicID: clinicID,
		Role:     role,
		ClientID: claims.AuthorizedParty,
		Token:    rawToken,
	}, nil
}

// Can indica si el rol del actor concede el permiso.
func (a Actor) Can(permission Permission) bool {
	return RoleHasPermission(a.Role, permission)
}

// IsService indica si el actor es la cuenta de servicio de clientID.
func (a Actor) IsService(clientID string) bool {
	return a.Role == RoleService && a.ClientID == clientID
}
