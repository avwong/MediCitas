package agenda

import (
	"context"
	"errors"
	"testing"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
)

const (
	clinicA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	clinicB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	userID  = "44444444-aaaa-4444-8444-444444444444"
)

// recordingStore only records calls; the SQL is exercised by integration
// tests against the real database.
type recordingStore struct {
	CatalogStore
	createdServices      []NewMedicalService
	createdPractitioners []NewPractitioner
	serviceIDs           []string
}

func (s *recordingStore) CreateService(_ context.Context, clinicID string, in NewMedicalService) (MedicalService, error) {
	s.createdServices = append(s.createdServices, in)
	return MedicalService{ClinicID: clinicID, Name: in.Name}, nil
}

func (s *recordingStore) CreatePractitioner(
	_ context.Context,
	clinicID string,
	in NewPractitioner,
	serviceIDs []string,
) (Practitioner, error) {
	s.createdPractitioners = append(s.createdPractitioners, in)
	s.serviceIDs = serviceIDs
	return Practitioner{ClinicID: clinicID, UserSubject: in.UserSubject}, nil
}

type fakeDirectory struct {
	user StaffUser
	err  error
}

func (d fakeDirectory) GetUser(context.Context, auth.Actor, string) (StaffUser, error) {
	return d.user, d.err
}

var clinicAdmin = auth.Actor{Subject: "admin", ClinicID: clinicA, Role: auth.RoleClinicAdmin}

func TestCreateServiceValidation(t *testing.T) {
	store := &recordingStore{}
	catalog := NewCatalog(store, fakeDirectory{})

	invalid := []NewMedicalService{
		{Name: "", DurationMinutes: 30, Specialty: "General"},
		{Name: "Consulta", DurationMinutes: 0, Specialty: "General"},
		{Name: "Consulta", DurationMinutes: 481, Specialty: "General"},
		{Name: "Consulta", DurationMinutes: 30, Specialty: " "},
	}

	for _, in := range invalid {
		if _, err := catalog.CreateService(context.Background(), clinicAdmin, in); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%+v: error = %v, want invalid input", in, err)
		}
	}

	if len(store.createdServices) != 0 {
		t.Fatal("invalid services must not reach the store")
	}

	service, err := catalog.CreateService(context.Background(), clinicAdmin, NewMedicalService{
		Name: "  Consulta general ", DurationMinutes: 30, Specialty: "Medicina general",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if service.ClinicID != clinicA || store.createdServices[0].Name != "Consulta general" {
		t.Error("the service must be created trimmed and in the actor's clinic")
	}
}

func TestCreatePractitionerRequiresAnActivePractitionerOfTheClinic(t *testing.T) {
	tests := []struct {
		name string
		user StaffUser
		dir  error
		want error
	}{
		{
			name: "reception user",
			user: StaffUser{ID: userID, ClinicID: clinicA, Subject: "s", Role: "reception", Active: true},
			want: apperr.ErrConflict,
		},
		{
			name: "inactive practitioner",
			user: StaffUser{ID: userID, ClinicID: clinicA, Subject: "s", Role: "practitioner", Active: false},
			want: apperr.ErrConflict,
		},
		{
			name: "user of another clinic",
			user: StaffUser{ID: userID, ClinicID: clinicB, Subject: "s", Role: "practitioner", Active: true},
			want: apperr.ErrNotFound,
		},
		{
			name: "tenancy unavailable",
			dir:  apperr.ErrUnavailable,
			want: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &recordingStore{}
			catalog := NewCatalog(store, fakeDirectory{user: tt.user, err: tt.dir})

			_, err := catalog.CreatePractitioner(context.Background(), clinicAdmin, NewPractitioner{
				UserID: userID, DisplayName: "Dra. Prueba", Specialty: "Medicina general",
			}, nil)

			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}

			if len(store.createdPractitioners) != 0 {
				t.Error("nothing may be written")
			}
		})
	}
}

func TestCreatePractitionerNormalizesServiceIDs(t *testing.T) {
	store := &recordingStore{}
	catalog := NewCatalog(store, fakeDirectory{user: StaffUser{
		ID: userID, ClinicID: clinicA, Subject: "subject-4", Role: "practitioner", Active: true,
	}})

	_, err := catalog.CreatePractitioner(context.Background(), clinicAdmin, NewPractitioner{
		UserID: userID, DisplayName: "Dra. Prueba", Specialty: "Medicina general",
	}, []string{
		"D1D1D1D1-0000-4000-8000-000000000001",
		"d1d1d1d1-0000-4000-8000-000000000001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.serviceIDs) != 1 || store.serviceIDs[0] != "d1d1d1d1-0000-4000-8000-000000000001" {
		t.Errorf("service ids = %v, want one normalized id", store.serviceIDs)
	}

	if store.createdPractitioners[0].UserSubject != "subject-4" {
		t.Error("the practitioner must keep the Keycloak subject from tenancy")
	}

	_, err = catalog.CreatePractitioner(context.Background(), clinicAdmin, NewPractitioner{
		UserID: userID, DisplayName: "Dra. Prueba", Specialty: "Medicina general",
	}, []string{"not-an-id"})
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("error = %v, want invalid input for a malformed service id", err)
	}
}

func TestMalformedIDsAreRejectedBeforeTheStore(t *testing.T) {
	catalog := NewCatalog(&recordingStore{}, fakeDirectory{})

	if _, err := catalog.GetService(context.Background(), clinicAdmin, "1 OR 1=1"); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("GetService: error = %v, want invalid input", err)
	}

	if _, err := catalog.GetPractitioner(context.Background(), clinicAdmin, "abc"); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("GetPractitioner: error = %v, want invalid input", err)
	}

	if err := catalog.ProvisionClinic(context.Background(), clinicA, "Mars/Olympus"); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("ProvisionClinic: error = %v, want invalid input for an unknown time zone", err)
	}
}
