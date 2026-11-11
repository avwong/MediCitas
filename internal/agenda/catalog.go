package agenda

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/ids"
)

const (
	maxServiceMinutes = 480
	maxNameLength     = 120
	maxDisplayLength  = 160
)

// CatalogStore persists clinics, services and practitioners. Every method
// that takes a clinicID only sees rows of that clinic (I5).
type CatalogStore interface {
	UpsertClinic(ctx context.Context, clinicID, timezone string) error
	DeleteClinic(ctx context.Context, clinicID string) error

	CreateService(ctx context.Context, clinicID string, in NewMedicalService) (MedicalService, error)
	ListServices(ctx context.Context, clinicID string) ([]MedicalService, error)
	GetService(ctx context.Context, clinicID, serviceID string) (MedicalService, error)
	DeactivateService(ctx context.Context, clinicID, serviceID string) error

	CreatePractitioner(ctx context.Context, clinicID string, in NewPractitioner, serviceIDs []string) (Practitioner, error)
	ListPractitioners(ctx context.Context, clinicID string) ([]Practitioner, error)
	GetPractitioner(ctx context.Context, clinicID, practitionerID string) (Practitioner, error)
	ReplacePractitionerServices(ctx context.Context, clinicID, practitionerID string, serviceIDs []string) (Practitioner, error)
}

// StaffDirectory looks up users in the tenancy service on behalf of actor,
// relaying the actor's token so tenancy applies the same clinic isolation.
type StaffDirectory interface {
	GetUser(ctx context.Context, actor auth.Actor, userID string) (StaffUser, error)
}

// Catalog implements RF3 and RF4 and the clinic provisioning that tenancy
// triggers when a clinic is created.
type Catalog struct {
	store CatalogStore
	staff StaffDirectory
}

// NewCatalog returns the catalog use cases.
func NewCatalog(store CatalogStore, staff StaffDirectory) *Catalog {
	return &Catalog{store: store, staff: staff}
}

// ProvisionClinic registers (or updates) the local projection of a clinic.
// It is idempotent so tenancy can retry it.
func (c *Catalog) ProvisionClinic(ctx context.Context, clinicID, timezone string) error {
	clinicID, ok := ids.Normalize(clinicID)
	if !ok {
		return apperr.Invalid("invalid clinic id")
	}

	timezone = strings.TrimSpace(timezone)
	if _, err := time.LoadLocation(timezone); err != nil || timezone == "" {
		return apperr.Invalid("invalid time zone")
	}

	return c.store.UpsertClinic(ctx, clinicID, timezone)
}

// DeprovisionClinic removes the local projection of a clinic that was never
// used. It is the compensation step of tenancy's clinic creation saga.
func (c *Catalog) DeprovisionClinic(ctx context.Context, clinicID string) error {
	clinicID, ok := ids.Normalize(clinicID)
	if !ok {
		return apperr.Invalid("invalid clinic id")
	}

	return c.store.DeleteClinic(ctx, clinicID)
}

// CreateService adds a service to the actor's clinic catalog (RF3).
func (c *Catalog) CreateService(ctx context.Context, actor auth.Actor, in NewMedicalService) (MedicalService, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Specialty = strings.TrimSpace(in.Specialty)

	switch {
	case !validText(in.Name, maxNameLength):
		return MedicalService{}, apperr.Invalid("name is required and must have at most 120 characters")
	case !validText(in.Specialty, maxNameLength):
		return MedicalService{}, apperr.Invalid("specialty is required and must have at most 120 characters")
	case in.DurationMinutes <= 0 || in.DurationMinutes > maxServiceMinutes:
		return MedicalService{}, apperr.Invalid("duration_minutes must be between 1 and 480")
	}

	return c.store.CreateService(ctx, actor.ClinicID, in)
}

// ListServices returns the actor's clinic catalog.
func (c *Catalog) ListServices(ctx context.Context, actor auth.Actor) ([]MedicalService, error) {
	return c.store.ListServices(ctx, actor.ClinicID)
}

// GetService returns one service of the actor's clinic.
func (c *Catalog) GetService(ctx context.Context, actor auth.Actor, serviceID string) (MedicalService, error) {
	serviceID, ok := ids.Normalize(serviceID)
	if !ok {
		return MedicalService{}, apperr.Invalid("invalid service id")
	}

	return c.store.GetService(ctx, actor.ClinicID, serviceID)
}

// DeactivateService withdraws a service from the catalog (logical delete).
// Existing associations and appointments keep referring to it.
func (c *Catalog) DeactivateService(ctx context.Context, actor auth.Actor, serviceID string) error {
	serviceID, ok := ids.Normalize(serviceID)
	if !ok {
		return apperr.Invalid("invalid service id")
	}

	return c.store.DeactivateService(ctx, actor.ClinicID, serviceID)
}

// CreatePractitioner registers a practitioner and the services they attend
// (RF4). The user must exist in the actor's clinic with the practitioner role,
// which is checked against the tenancy service with the actor's own token.
func (c *Catalog) CreatePractitioner(
	ctx context.Context,
	actor auth.Actor,
	in NewPractitioner,
	serviceIDs []string,
) (Practitioner, error) {
	userID, ok := ids.Normalize(in.UserID)
	if !ok {
		return Practitioner{}, apperr.Invalid("invalid user id")
	}

	in.UserID = userID
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Specialty = strings.TrimSpace(in.Specialty)

	switch {
	case !validText(in.DisplayName, maxDisplayLength):
		return Practitioner{}, apperr.Invalid("display_name is required and must have at most 160 characters")
	case !validText(in.Specialty, maxNameLength):
		return Practitioner{}, apperr.Invalid("specialty is required and must have at most 120 characters")
	}

	serviceIDs, ok = ids.NormalizeAll(serviceIDs)
	if !ok {
		return Practitioner{}, apperr.Invalid("service_ids must contain valid ids")
	}

	user, err := c.staff.GetUser(ctx, actor, in.UserID)
	if err != nil {
		return Practitioner{}, err
	}

	if user.ClinicID != actor.ClinicID {
		// Defense in depth: tenancy already scopes the lookup to the clinic.
		return Practitioner{}, apperr.NotFound("user not found")
	}

	if !user.Active || user.Role != string(auth.RolePractitioner) {
		return Practitioner{}, apperr.Conflict("the user is not an active practitioner")
	}

	in.UserSubject = user.Subject

	return c.store.CreatePractitioner(ctx, actor.ClinicID, in, serviceIDs)
}

// ListPractitioners returns the practitioners of the actor's clinic.
func (c *Catalog) ListPractitioners(ctx context.Context, actor auth.Actor) ([]Practitioner, error) {
	return c.store.ListPractitioners(ctx, actor.ClinicID)
}

// GetPractitioner returns one practitioner of the actor's clinic.
func (c *Catalog) GetPractitioner(ctx context.Context, actor auth.Actor, practitionerID string) (Practitioner, error) {
	practitionerID, ok := ids.Normalize(practitionerID)
	if !ok {
		return Practitioner{}, apperr.Invalid("invalid practitioner id")
	}

	return c.store.GetPractitioner(ctx, actor.ClinicID, practitionerID)
}

// ReplacePractitionerServices sets the services a practitioner attends. Every
// service must belong to the clinic, be active and require the practitioner's
// specialty.
func (c *Catalog) ReplacePractitionerServices(
	ctx context.Context,
	actor auth.Actor,
	practitionerID string,
	serviceIDs []string,
) (Practitioner, error) {
	practitionerID, ok := ids.Normalize(practitionerID)
	if !ok {
		return Practitioner{}, apperr.Invalid("invalid practitioner id")
	}

	serviceIDs, ok = ids.NormalizeAll(serviceIDs)
	if !ok {
		return Practitioner{}, apperr.Invalid("service_ids must contain valid ids")
	}

	return c.store.ReplacePractitionerServices(ctx, actor.ClinicID, practitionerID, serviceIDs)
}

func validText(value string, maxLength int) bool {
	return value != "" && utf8.RuneCountInString(value) <= maxLength
}
