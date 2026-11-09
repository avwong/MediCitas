package auth

import (
	"errors"
	"strings"

	"github.com/avwong/MediCitas/internal/platform/ids"
)

var (
	// ErrNoSubject means the token has no subject.
	ErrNoSubject = errors.New("token has no subject")
	// ErrNoDomainRole means the token carries no MediCitas role.
	ErrNoDomainRole = errors.New("token carries no domain role")
	// ErrAmbiguousRole means the token carries more than one MediCitas role.
	ErrAmbiguousRole = errors.New("token carries more than one domain role")
	// ErrNoClinic means a clinic role arrived without a valid clinic claim.
	ErrNoClinic = errors.New("clinic role without a valid clinic claim")
)

// Claims are the access-token claims MediCitas relies on.
type Claims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	AuthorizedParty   string `json:"azp"`
	// ClinicID is written by the tenancy service as a Keycloak user attribute
	// and added to the token by a protocol mapper. Because it is signed, no
	// service ever takes the clinic from the request.
	ClinicID    string `json:"clinic_id"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Actor is the authenticated caller of a request.
type Actor struct {
	Subject  string
	Username string
	// ClinicID is empty for platform administrators and service accounts.
	ClinicID string
	Role     Role
	// ClientID is the Keycloak client that obtained the token (azp).
	ClientID string
	// Token is the raw bearer token, kept to relay the user's identity when a
	// service calls another one on the user's behalf.
	Token string
}

// NewActor validates the claims and builds the actor. A token must carry
// exactly one domain role, and clinic roles must carry a clinic.
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

// Can reports whether the actor's role grants permission.
func (a Actor) Can(permission Permission) bool {
	return RoleHasPermission(a.Role, permission)
}

// IsService reports whether the actor is the service account of clientID.
func (a Actor) IsService(clientID string) bool {
	return a.Role == RoleService && a.ClientID == clientID
}
