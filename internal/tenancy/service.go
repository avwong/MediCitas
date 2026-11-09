package tenancy

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/ids"
)

var (
	clinicIdentifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	usernamePattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
)

// Store persists clinics and users in tenancy_db.
type Store interface {
	InsertClinic(ctx context.Context, clinic Clinic) (Clinic, error)
	ActivateClinic(ctx context.Context, clinicID string, admin User) (Clinic, User, error)
	DeleteProvisioningClinic(ctx context.Context, clinicID string) error
	ListClinics(ctx context.Context) ([]Clinic, error)

	InsertUser(ctx context.Context, user User) (User, error)
	GetUser(ctx context.Context, clinicID, userID string) (User, error)
	FindUserBySubject(ctx context.Context, subject string) (User, error)
	ListUsers(ctx context.Context, clinicID string) ([]User, error)
	SetUserRole(ctx context.Context, clinicID, userID string, role auth.Role) error
	DeactivateUser(ctx context.Context, clinicID, userID string) error
}

// IdentityProvider manages identities in Keycloak.
type IdentityProvider interface {
	// CreateUser creates an enabled user with the clinic attribute and the
	// realm role, and returns its subject.
	CreateUser(ctx context.Context, identity NewIdentity) (string, error)
	DeleteUser(ctx context.Context, subject string) error
	ReplaceRole(ctx context.Context, subject string, role auth.Role) error
	// Disable blocks new logins and ends the user's sessions.
	Disable(ctx context.Context, subject string) error
}

// AgendaProvisioner creates the projection of a clinic in the agenda service.
type AgendaProvisioner interface {
	ProvisionClinic(ctx context.Context, clinicID, timezone string) error
	DeprovisionClinic(ctx context.Context, clinicID string) error
}

// Service implements RF1 and RF2.
type Service struct {
	store  Store
	idp    IdentityProvider
	agenda AgendaProvisioner
	logger *slog.Logger
}

// NewService returns the tenancy use cases.
func NewService(store Store, idp IdentityProvider, agenda AgendaProvisioner, logger *slog.Logger) *Service {
	return &Service{store: store, idp: idp, agenda: agenda, logger: logger}
}

// CreateClinic registers a clinic together with its first administrator
// (RF1).
//
// It spans three systems that share no transaction: tenancy_db, the agenda
// service and Keycloak. It runs as a saga: each step that fails undoes the
// previous ones, so a failed request leaves no half-created clinic behind.
func (s *Service) CreateClinic(ctx context.Context, in CreateClinicInput) (Clinic, User, error) {
	in.Identifier = strings.ToLower(strings.TrimSpace(in.Identifier))
	in.Name = strings.TrimSpace(in.Name)
	in.Timezone = strings.TrimSpace(in.Timezone)

	switch {
	case !clinicIdentifierPattern.MatchString(in.Identifier):
		return Clinic{}, User{}, apperr.Invalid(
			"identifier must have 2 to 64 lowercase letters, digits or hyphens",
		)
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 160:
		return Clinic{}, User{}, apperr.Invalid("name is required and must have at most 160 characters")
	case !validTimezone(in.Timezone):
		return Clinic{}, User{}, apperr.Invalid("timezone must be an IANA time zone, such as America/Costa_Rica")
	}

	admin, err := normalizeAccount(in.Admin)
	if err != nil {
		return Clinic{}, User{}, err
	}

	// Step 1: register the clinic, not yet active.
	clinic, err := s.store.InsertClinic(ctx, Clinic{
		Identifier: in.Identifier,
		Name:       in.Name,
		Timezone:   in.Timezone,
		Status:     ClinicProvisioning,
	})
	if err != nil {
		return Clinic{}, User{}, err
	}

	clinicID := clinic.ID

	undoClinic := func(ctx context.Context) error {
		return s.store.DeleteProvisioningClinic(ctx, clinicID)
	}

	// Step 2: provision the clinic in the agenda.
	if err := s.agenda.ProvisionClinic(ctx, clinicID, clinic.Timezone); err != nil {
		s.compensate(ctx, "delete clinic", undoClinic)
		return Clinic{}, User{}, fmt.Errorf("provision clinic in agenda: %w", err)
	}

	undoAgenda := func(ctx context.Context) error {
		return s.agenda.DeprovisionClinic(ctx, clinicID)
	}

	// Step 3: create the administrator in Keycloak.
	subject, err := s.idp.CreateUser(ctx, NewIdentity{
		Account:  admin,
		ClinicID: clinicID,
		Role:     auth.RoleClinicAdmin,
	})
	if err != nil {
		s.compensate(ctx, "deprovision agenda", undoAgenda)
		s.compensate(ctx, "delete clinic", undoClinic)
		return Clinic{}, User{}, err
	}

	// Step 4: record the administrator and activate the clinic atomically.
	activeClinic, adminUser, err := s.store.ActivateClinic(ctx, clinicID, User{
		ClinicID: clinicID,
		Subject:  subject,
		Username: admin.Username,
		Role:     auth.RoleClinicAdmin,
		Active:   true,
	})
	if err != nil {
		s.compensate(ctx, "delete identity", func(ctx context.Context) error {
			return s.idp.DeleteUser(ctx, subject)
		})
		s.compensate(ctx, "deprovision agenda", undoAgenda)
		s.compensate(ctx, "delete clinic", undoClinic)
		return Clinic{}, User{}, err
	}

	return activeClinic, adminUser, nil
}

// ListClinics returns every clinic (platform administrators only).
func (s *Service) ListClinics(ctx context.Context) ([]Clinic, error) {
	return s.store.ListClinics(ctx)
}

// CreateUser creates a user in the actor's clinic (RF2). The identity is
// created in Keycloak first and compensated if the local record fails.
func (s *Service) CreateUser(ctx context.Context, actor auth.Actor, in CreateUserInput) (User, error) {
	role, ok := auth.ParseClinicRole(in.Role)
	if !ok {
		return User{}, apperr.Invalid("role must be clinic_admin, reception or practitioner")
	}

	account, err := normalizeAccount(in.Account)
	if err != nil {
		return User{}, err
	}

	subject, err := s.idp.CreateUser(ctx, NewIdentity{
		Account:  account,
		ClinicID: actor.ClinicID,
		Role:     role,
	})
	if err != nil {
		return User{}, err
	}

	user, err := s.store.InsertUser(ctx, User{
		ClinicID: actor.ClinicID,
		Subject:  subject,
		Username: account.Username,
		Role:     role,
		Active:   true,
	})
	if err != nil {
		s.compensate(ctx, "delete identity", func(ctx context.Context) error {
			return s.idp.DeleteUser(ctx, subject)
		})
		return User{}, err
	}

	return user, nil
}

// GetUser returns a user of the actor's clinic.
func (s *Service) GetUser(ctx context.Context, actor auth.Actor, userID string) (User, error) {
	userID, ok := ids.Normalize(userID)
	if !ok {
		return User{}, apperr.Invalid("invalid user id")
	}

	return s.store.GetUser(ctx, actor.ClinicID, userID)
}

// ListUsers returns the users of the actor's clinic.
func (s *Service) ListUsers(ctx context.Context, actor auth.Actor) ([]User, error) {
	return s.store.ListUsers(ctx, actor.ClinicID)
}

// Me returns the local record of the caller, if any.
func (s *Service) Me(ctx context.Context, actor auth.Actor) (User, error) {
	return s.store.FindUserBySubject(ctx, actor.Subject)
}

// UpdateUserRole changes the role of a user of the actor's clinic (RF2).
// Keycloak is updated first because it is the source of truth; the local copy
// follows. Retrying after a partial failure converges to the same state.
func (s *Service) UpdateUserRole(ctx context.Context, actor auth.Actor, userID, roleName string) (User, error) {
	role, ok := auth.ParseClinicRole(roleName)
	if !ok {
		return User{}, apperr.Invalid("role must be clinic_admin, reception or practitioner")
	}

	user, err := s.modifiableUser(ctx, actor, userID)
	if err != nil {
		return User{}, err
	}

	if err := s.idp.ReplaceRole(ctx, user.Subject, role); err != nil {
		return User{}, err
	}

	if err := s.store.SetUserRole(ctx, actor.ClinicID, user.ID, role); err != nil {
		return User{}, err
	}

	user.Role = role

	return user, nil
}

// DeactivateUser is the logical delete of RF2. The user is disabled in
// Keycloak first, which blocks new logins and ends their sessions; tokens
// already issued expire within the access-token lifespan.
func (s *Service) DeactivateUser(ctx context.Context, actor auth.Actor, userID string) error {
	user, err := s.modifiableUser(ctx, actor, userID)
	if err != nil {
		return err
	}

	if err := s.idp.Disable(ctx, user.Subject); err != nil {
		return err
	}

	return s.store.DeactivateUser(ctx, actor.ClinicID, user.ID)
}

// modifiableUser returns an active user of the actor's clinic other than the
// actor, so an administrator cannot lock themselves out.
func (s *Service) modifiableUser(ctx context.Context, actor auth.Actor, userID string) (User, error) {
	userID, ok := ids.Normalize(userID)
	if !ok {
		return User{}, apperr.Invalid("invalid user id")
	}

	user, err := s.store.GetUser(ctx, actor.ClinicID, userID)
	if err != nil {
		return User{}, err
	}

	if user.Subject == actor.Subject {
		return User{}, apperr.Conflict("you cannot modify your own account")
	}

	if !user.Active {
		return User{}, apperr.Conflict("the user is inactive")
	}

	return user, nil
}

// compensate runs an undo step of a saga. It uses a fresh context so a
// cancelled request still gets cleaned up, and logs failures: a failed
// compensation leaves state that an operator must fix.
func (s *Service) compensate(ctx context.Context, step string, undo func(context.Context) error) {
	undoCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := undo(undoCtx); err != nil {
		s.logger.ErrorContext(ctx, "saga compensation failed", "step", step, "error", err)
	}
}

func normalizeAccount(account Account) (Account, error) {
	account.Username = strings.ToLower(strings.TrimSpace(account.Username))
	account.Email = strings.TrimSpace(account.Email)
	account.FirstName = strings.TrimSpace(account.FirstName)
	account.LastName = strings.TrimSpace(account.LastName)

	switch {
	case !usernamePattern.MatchString(account.Username):
		return Account{}, apperr.Invalid(
			"username must have 3 to 64 lowercase letters, digits, dots, hyphens or underscores",
		)
	case !validEmail(account.Email):
		return Account{}, apperr.Invalid("email is invalid")
	case account.FirstName == "" || utf8.RuneCountInString(account.FirstName) > 100:
		return Account{}, apperr.Invalid("first_name is required and must have at most 100 characters")
	case account.LastName == "" || utf8.RuneCountInString(account.LastName) > 100:
		return Account{}, apperr.Invalid("last_name is required and must have at most 100 characters")
	case utf8.RuneCountInString(account.Password) < 8 || utf8.RuneCountInString(account.Password) > 128:
		return Account{}, apperr.Invalid("password must have between 8 and 128 characters")
	}

	return account, nil
}

func validEmail(value string) bool {
	if value == "" || len(value) > 254 {
		return false
	}

	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func validTimezone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}

	_, err := time.LoadLocation(name)
	return err == nil
}
