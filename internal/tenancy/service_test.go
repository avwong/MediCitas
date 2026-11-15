package tenancy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
)

const testClinicID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

// fakeStore registra lo que la saga hizo en la base de datos.
type fakeStore struct {
	inserted       []Clinic
	deletedClinics []string
	activated      []string
	activateErr    error
	users          map[string]User
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[string]User{}}
}

func (s *fakeStore) InsertClinic(_ context.Context, clinic Clinic) (Clinic, error) {
	clinic.ID = testClinicID
	s.inserted = append(s.inserted, clinic)
	return clinic, nil
}

func (s *fakeStore) ActivateClinic(_ context.Context, clinicID string, admin User) (Clinic, User, error) {
	if s.activateErr != nil {
		return Clinic{}, User{}, s.activateErr
	}

	s.activated = append(s.activated, clinicID)
	admin.ID = "admin-user-id"
	return Clinic{ID: clinicID, Status: ClinicActive}, admin, nil
}

func (s *fakeStore) DeleteProvisioningClinic(_ context.Context, clinicID string) error {
	s.deletedClinics = append(s.deletedClinics, clinicID)
	return nil
}

func (s *fakeStore) ListClinics(context.Context) ([]Clinic, error) { return nil, nil }

func (s *fakeStore) InsertUser(_ context.Context, user User) (User, error) {
	user.ID = "new-user-id"
	s.users[user.ID] = user
	return user, nil
}

func (s *fakeStore) GetUser(_ context.Context, clinicID, userID string) (User, error) {
	user, ok := s.users[userID]
	if !ok || user.ClinicID != clinicID {
		return User{}, apperr.ErrNotFound
	}

	return user, nil
}

func (s *fakeStore) FindUserBySubject(context.Context, string) (User, error) {
	return User{}, apperr.ErrNotFound
}

func (s *fakeStore) ListUsers(context.Context, string) ([]User, error) { return nil, nil }

func (s *fakeStore) SetUserRole(_ context.Context, _, userID string, role auth.Role) error {
	user := s.users[userID]
	user.Role = role
	s.users[userID] = user
	return nil
}

func (s *fakeStore) DeactivateUser(_ context.Context, _, userID string) error {
	user := s.users[userID]
	user.Active = false
	s.users[userID] = user
	return nil
}

type fakeIdP struct {
	createErr error
	created   []NewIdentity
	deleted   []string
	roles     map[string]auth.Role
	disabled  []string
}

func (f *fakeIdP) CreateUser(_ context.Context, identity NewIdentity) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}

	f.created = append(f.created, identity)
	return "subject-1", nil
}

func (f *fakeIdP) DeleteUser(_ context.Context, subject string) error {
	f.deleted = append(f.deleted, subject)
	return nil
}

func (f *fakeIdP) ReplaceRole(_ context.Context, subject string, role auth.Role) error {
	if f.roles == nil {
		f.roles = map[string]auth.Role{}
	}

	f.roles[subject] = role
	return nil
}

func (f *fakeIdP) Disable(_ context.Context, subject string) error {
	f.disabled = append(f.disabled, subject)
	return nil
}

type fakeAgenda struct {
	provisionErr  error
	provisioned   []string
	deprovisioned []string
	activeErr     error
	activeChanges map[string]bool
}

func (f *fakeAgenda) SetPractitionerActive(_ context.Context, userID string, active bool) error {
	if f.activeErr != nil {
		return f.activeErr
	}

	if f.activeChanges == nil {
		f.activeChanges = map[string]bool{}
	}

	f.activeChanges[userID] = active
	return nil
}

func (f *fakeAgenda) ProvisionClinic(_ context.Context, clinicID, _ string) error {
	if f.provisionErr != nil {
		return f.provisionErr
	}

	f.provisioned = append(f.provisioned, clinicID)
	return nil
}

func (f *fakeAgenda) DeprovisionClinic(_ context.Context, clinicID string) error {
	f.deprovisioned = append(f.deprovisioned, clinicID)
	return nil
}

func newTestService() (*Service, *fakeStore, *fakeIdP, *fakeAgenda) {
	store := newFakeStore()
	idp := &fakeIdP{}
	agenda := &fakeAgenda{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return NewService(store, idp, agenda, logger), store, idp, agenda
}

func validClinicInput() CreateClinicInput {
	return CreateClinicInput{
		Identifier: "clinic-sur",
		Name:       "Clínica Sur",
		Timezone:   "America/Costa_Rica",
		Admin: Account{
			Username:  "sur.admin",
			Email:     "sur.admin@example.com",
			FirstName: "Sur",
			LastName:  "Admin",
			Password:  "Password123!",
		},
	}
}

func TestCreateClinicHappyPath(t *testing.T) {
	service, store, idp, agenda := newTestService()

	clinic, admin, err := service.CreateClinic(context.Background(), validClinicInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clinic.Status != ClinicActive {
		t.Errorf("clinic status = %q, want active", clinic.Status)
	}

	if len(agenda.provisioned) != 1 || len(store.activated) != 1 {
		t.Errorf("every saga step must run once: provisioned=%v activated=%v", agenda.provisioned, store.activated)
	}

	if len(idp.created) != 1 {
		t.Fatalf("administrator was not created in Keycloak")
	}

	created := idp.created[0]
	if created.Role != auth.RoleClinicAdmin || created.ClinicID != testClinicID {
		t.Errorf("administrator got role %q and clinic %q", created.Role, created.ClinicID)
	}

	if admin.Subject != "subject-1" {
		t.Errorf("admin subject = %q, want the Keycloak subject", admin.Subject)
	}
}

func TestCreateClinicCompensatesWhenAgendaFails(t *testing.T) {
	service, store, idp, agenda := newTestService()
	agenda.provisionErr = apperr.ErrUnavailable

	_, _, err := service.CreateClinic(context.Background(), validClinicInput())
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}

	if len(store.deletedClinics) != 1 {
		t.Error("the provisioning clinic must be deleted")
	}

	if len(idp.created) != 0 {
		t.Error("no identity may be created after a failed step")
	}
}

func TestCreateClinicCompensatesWhenKeycloakFails(t *testing.T) {
	service, store, idp, agenda := newTestService()
	idp.createErr = apperr.Conflict("username or email already exists")

	_, _, err := service.CreateClinic(context.Background(), validClinicInput())
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}

	if len(agenda.deprovisioned) != 1 || len(store.deletedClinics) != 1 {
		t.Errorf("agenda and clinic must be undone: deprovisioned=%v deleted=%v",
			agenda.deprovisioned, store.deletedClinics)
	}
}

func TestCreateClinicCompensatesWhenActivationFails(t *testing.T) {
	service, store, idp, agenda := newTestService()
	store.activateErr = apperr.ErrConflict

	if _, _, err := service.CreateClinic(context.Background(), validClinicInput()); err == nil {
		t.Fatal("expected an error")
	}

	if len(idp.deleted) != 1 || idp.deleted[0] != "subject-1" {
		t.Errorf("the Keycloak user must be deleted, deleted=%v", idp.deleted)
	}

	if len(agenda.deprovisioned) != 1 || len(store.deletedClinics) != 1 {
		t.Error("agenda and clinic must be undone")
	}
}

func TestCreateClinicValidation(t *testing.T) {
	cases := map[string]func(*CreateClinicInput){
		"bad identifier": func(in *CreateClinicInput) { in.Identifier = "Clínica Sur" },
		"bad timezone":   func(in *CreateClinicInput) { in.Timezone = "Mars/Olympus" },
		"empty name":     func(in *CreateClinicInput) { in.Name = "  " },
		"bad email":      func(in *CreateClinicInput) { in.Admin.Email = "not-an-email" },
		"short password": func(in *CreateClinicInput) { in.Admin.Password = "short" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			service, store, _, _ := newTestService()
			in := validClinicInput()
			mutate(&in)

			_, _, err := service.CreateClinic(context.Background(), in)
			if !errors.Is(err, apperr.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}

			if len(store.inserted) != 0 {
				t.Error("nothing may be written when the input is invalid")
			}
		})
	}
}

func TestCreateUserRejectsPrivilegedRoles(t *testing.T) {
	service, _, idp, _ := newTestService()
	actor := auth.Actor{Subject: "admin", ClinicID: testClinicID, Role: auth.RoleClinicAdmin}

	for _, role := range []string{"platform_admin", "service"} {
		_, err := service.CreateUser(context.Background(), actor, CreateUserInput{
			Account: validClinicInput().Admin,
			Role:    role,
		})
		if !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("role %q: error = %v, want invalid input", role, err)
		}
	}

	if len(idp.created) != 0 {
		t.Error("no identity may be created with a privileged role")
	}
}

func TestCreateUserUsesTheActorsClinic(t *testing.T) {
	service, _, idp, _ := newTestService()
	actor := auth.Actor{Subject: "admin", ClinicID: testClinicID, Role: auth.RoleClinicAdmin}

	user, err := service.CreateUser(context.Background(), actor, CreateUserInput{
		Account: validClinicInput().Admin,
		Role:    "reception",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if user.ClinicID != testClinicID || idp.created[0].ClinicID != testClinicID {
		t.Error("the user must belong to the clinic in the actor's token")
	}
}

func TestAdministratorCannotModifyThemselves(t *testing.T) {
	service, store, idp, _ := newTestService()

	const userID = "11111111-2222-4333-8444-555555555555"
	store.users[userID] = User{
		ID:       userID,
		ClinicID: testClinicID,
		Subject:  "admin-subject",
		Role:     auth.RoleClinicAdmin,
		Active:   true,
	}
	actor := auth.Actor{Subject: "admin-subject", ClinicID: testClinicID, Role: auth.RoleClinicAdmin}

	err := service.DeactivateUser(context.Background(), actor, userID)
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}

	if len(idp.disabled) != 0 {
		t.Error("the administrator must not be disabled")
	}
}

func TestUserOfAnotherClinicIsNotFound(t *testing.T) {
	service, store, idp, _ := newTestService()

	const userID = "11111111-2222-4333-8444-555555555555"
	store.users[userID] = User{
		ID:       userID,
		ClinicID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		Subject:  "other-clinic-user",
		Role:     auth.RoleReception,
		Active:   true,
	}

	actor := auth.Actor{Subject: "admin", ClinicID: testClinicID, Role: auth.RoleClinicAdmin}

	_, err := service.UpdateUserRole(context.Background(), actor, userID, "clinic_admin")
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}

	if len(idp.roles) != 0 {
		t.Error("no role may change in Keycloak for a user of another clinic")
	}
}

const staffUserID = "11111111-2222-4333-8444-555555555555"

var testAdmin = auth.Actor{Subject: "admin", ClinicID: testClinicID, Role: auth.RoleClinicAdmin}

func addStaffUser(store *fakeStore, role auth.Role) {
	store.users[staffUserID] = User{
		ID:       staffUserID,
		ClinicID: testClinicID,
		Subject:  "staff-subject",
		Role:     role,
		Active:   true,
	}
}

func TestRoleChangeKeepsThePractitionerInAgendaInSync(t *testing.T) {
	cases := []struct {
		name       string
		from, to   auth.Role
		wantChange bool
		wantActive bool
	}{
		{"deja de ser profesional", auth.RolePractitioner, auth.RoleReception, true, false},
		{"vuelve a ser profesional", auth.RoleReception, auth.RolePractitioner, true, true},
		{"nunca fue profesional", auth.RoleReception, auth.RoleClinicAdmin, false, false},
		{"sigue siendo profesional", auth.RolePractitioner, auth.RolePractitioner, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, store, _, agenda := newTestService()
			addStaffUser(store, tc.from)

			if _, err := service.UpdateUserRole(context.Background(), testAdmin, staffUserID, string(tc.to)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			active, changed := agenda.activeChanges[staffUserID]
			if changed != tc.wantChange || active != tc.wantActive {
				t.Errorf("agenda change = (%v, active %v), want (%v, active %v)", changed, active, tc.wantChange, tc.wantActive)
			}
		})
	}
}

// Si Agenda no responde, la copia local conserva el rol anterior, de modo que
// repetir la petición vuelve a intentar el aviso a Agenda.
func TestRoleChangeCanBeRetriedWhenAgendaFails(t *testing.T) {
	service, store, _, agenda := newTestService()
	addStaffUser(store, auth.RolePractitioner)
	agenda.activeErr = apperr.ErrUnavailable

	_, err := service.UpdateUserRole(context.Background(), testAdmin, staffUserID, "reception")
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}

	if store.users[staffUserID].Role != auth.RolePractitioner {
		t.Fatal("the local role must not change until agenda has been updated")
	}

	agenda.activeErr = nil

	if _, err := service.UpdateUserRole(context.Background(), testAdmin, staffUserID, "reception"); err != nil {
		t.Fatalf("retry: unexpected error: %v", err)
	}

	if active, changed := agenda.activeChanges[staffUserID]; !changed || active {
		t.Error("the retry must deactivate the practitioner in agenda")
	}
}

func TestDeactivatingAPractitionerDeactivatesItInAgenda(t *testing.T) {
	service, store, idp, agenda := newTestService()
	addStaffUser(store, auth.RolePractitioner)

	if err := service.DeactivateUser(context.Background(), testAdmin, staffUserID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(idp.disabled) != 1 {
		t.Error("the user must be disabled in Keycloak")
	}

	if active, changed := agenda.activeChanges[staffUserID]; !changed || active {
		t.Error("the practitioner must be deactivated in agenda")
	}

	if store.users[staffUserID].Active {
		t.Error("the user must be inactive locally")
	}
}

func TestDeactivatingOtherRolesDoesNotCallAgenda(t *testing.T) {
	service, store, _, agenda := newTestService()
	addStaffUser(store, auth.RoleReception)

	if err := service.DeactivateUser(context.Background(), testAdmin, staffUserID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(agenda.activeChanges) != 0 {
		t.Error("agenda must not be called for a user that is not a practitioner")
	}
}
