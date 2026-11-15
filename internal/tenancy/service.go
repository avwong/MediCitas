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

// Store guarda las clínicas y los usuarios en tenancy_db.
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

// IdentityProvider administra las identidades en Keycloak.
type IdentityProvider interface {
	// CreateUser crea un usuario habilitado, con el atributo de clínica y el
	// rol de realm, y devuelve su subject.
	CreateUser(ctx context.Context, identity NewIdentity) (string, error)
	DeleteUser(ctx context.Context, subject string) error
	ReplaceRole(ctx context.Context, subject string, role auth.Role) error
	// Disable bloquea los inicios de sesión nuevos y cierra las sesiones del
	// usuario.
	Disable(ctx context.Context, subject string) error
}

// AgendaProvisioner mantiene en Agenda lo que depende de Tenancy: la
// proyección de cada clínica y si el profesional de un usuario sigue activo.
type AgendaProvisioner interface {
	ProvisionClinic(ctx context.Context, clinicID, timezone string) error
	DeprovisionClinic(ctx context.Context, clinicID string) error
	// SetPractitionerActive no falla si el usuario no tiene profesional en
	// Agenda.
	SetPractitionerActive(ctx context.Context, userID string, active bool) error
}

// Service implementa RF1 y RF2.
type Service struct {
	store  Store
	idp    IdentityProvider
	agenda AgendaProvisioner
	logger *slog.Logger
}

// NewService devuelve los casos de uso de Tenancy.
func NewService(store Store, idp IdentityProvider, agenda AgendaProvisioner, logger *slog.Logger) *Service {
	return &Service{store: store, idp: idp, agenda: agenda, logger: logger}
}

// CreateClinic registra una clínica junto con su primer administrador (RF1).
//
// Toca tres sistemas que no comparten transacción: tenancy_db, el servicio de
// Agenda y Keycloak. Se ejecuta como una saga: si un paso falla se deshacen
// los anteriores, de modo que una petición fallida no deja una clínica a medio
// crear.
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

	// Paso 1: registrar la clínica, todavía sin activar.
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

	// Paso 2: aprovisionar la clínica en Agenda.
	if err := s.agenda.ProvisionClinic(ctx, clinicID, clinic.Timezone); err != nil {
		s.compensate(ctx, "delete clinic", undoClinic)
		return Clinic{}, User{}, fmt.Errorf("provision clinic in agenda: %w", err)
	}

	undoAgenda := func(ctx context.Context) error {
		return s.agenda.DeprovisionClinic(ctx, clinicID)
	}

	// Paso 3: crear el administrador en Keycloak.
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

	// Paso 4: registrar el administrador y activar la clínica de forma
	// atómica.
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

// ListClinics devuelve todas las clínicas (solo para la administración de
// plataforma).
func (s *Service) ListClinics(ctx context.Context) ([]Clinic, error) {
	return s.store.ListClinics(ctx)
}

// CreateUser crea un usuario en la clínica del actor (RF2). La identidad se
// crea primero en Keycloak y se compensa si falla el registro local.
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

// GetUser devuelve un usuario de la clínica del actor.
func (s *Service) GetUser(ctx context.Context, actor auth.Actor, userID string) (User, error) {
	userID, ok := ids.Normalize(userID)
	if !ok {
		return User{}, apperr.Invalid("invalid user id")
	}

	return s.store.GetUser(ctx, actor.ClinicID, userID)
}

// ListUsers devuelve los usuarios de la clínica del actor.
func (s *Service) ListUsers(ctx context.Context, actor auth.Actor) ([]User, error) {
	return s.store.ListUsers(ctx, actor.ClinicID)
}

// Me devuelve el registro local de quien llama, si existe.
func (s *Service) Me(ctx context.Context, actor auth.Actor) (User, error) {
	return s.store.FindUserBySubject(ctx, actor.Subject)
}

// UpdateUserRole cambia el rol de un usuario de la clínica del actor (RF2).
// Keycloak se actualiza primero porque es la fuente de verdad; después Agenda,
// si el usuario deja de ser profesional o vuelve a serlo, y al final la copia
// local. Si falla a medias, reintentar lleva al mismo estado final: la copia
// local todavía tiene el rol anterior, así que se repiten los mismos pasos.
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

	wasPractitioner := user.Role == auth.RolePractitioner
	isPractitioner := role == auth.RolePractitioner

	if wasPractitioner != isPractitioner {
		if err := s.agenda.SetPractitionerActive(ctx, user.ID, isPractitioner); err != nil {
			return User{}, fmt.Errorf("update practitioner in agenda: %w", err)
		}
	}

	if err := s.store.SetUserRole(ctx, actor.ClinicID, user.ID, role); err != nil {
		return User{}, err
	}

	user.Role = role

	return user, nil
}

// DeactivateUser es la baja lógica de RF2. Primero se deshabilita al usuario
// en Keycloak, lo que bloquea los inicios de sesión nuevos y cierra sus
// sesiones; los tokens ya emitidos vencen dentro de la vida del token de
// acceso. Si era profesional, también se desactiva en Agenda. La copia local
// se marca al final, para que un fallo a medias se pueda reintentar.
func (s *Service) DeactivateUser(ctx context.Context, actor auth.Actor, userID string) error {
	user, err := s.modifiableUser(ctx, actor, userID)
	if err != nil {
		return err
	}

	if err := s.idp.Disable(ctx, user.Subject); err != nil {
		return err
	}

	if user.Role == auth.RolePractitioner {
		if err := s.agenda.SetPractitionerActive(ctx, user.ID, false); err != nil {
			return fmt.Errorf("deactivate practitioner in agenda: %w", err)
		}
	}

	return s.store.DeactivateUser(ctx, actor.ClinicID, user.ID)
}

// modifiableUser devuelve un usuario activo de la clínica del actor que no sea
// el propio actor, para que un administrador no pueda dejarse a sí mismo sin
// acceso.
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

// compensate ejecuta un paso de deshacer de una saga. Usa un contexto nuevo
// para que la limpieza ocurra aunque la petición se haya cancelado, y registra
// los fallos: una compensación fallida deja un estado que alguien debe
// corregir a mano.
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
