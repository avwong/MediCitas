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

// CatalogStore guarda clínicas, servicios y profesionales. Todo método que
// recibe un clinicID solo ve filas de esa clínica (I5).
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
	SetPractitionerActiveByUser(ctx context.Context, userID string, active bool) (clinicID, practitionerID string, err error)
}

// StaffDirectory busca usuarios en Tenancy en nombre del actor y reenvía su
// token, para que Tenancy aplique el mismo aislamiento entre clínicas.
type StaffDirectory interface {
	GetUser(ctx context.Context, actor auth.Actor, userID string) (StaffUser, error)
}

// Catalog implementa RF3 y RF4, y el aprovisionamiento que Tenancy dispara al
// crear una clínica.
type Catalog struct {
	store   CatalogStore
	staff   StaffDirectory
	agendas AgendaObserver
}

// AgendaObserver se entera de los cambios del catálogo que afectan a la
// disponibilidad ya calculada: el estado de un profesional, los servicios que
// atiende y el retiro de un servicio. Lo implementa *Scheduling, que invalida
// la caché de esas agendas (I8).
type AgendaObserver interface {
	InvalidateAgendas(ctx context.Context, clinicID string, practitionerIDs ...string)
	InvalidateService(ctx context.Context, clinicID, serviceID string)
}

// noAgendas es el observador cuando no hay ninguno registrado.
type noAgendas struct{}

func (noAgendas) InvalidateAgendas(context.Context, string, ...string) {}
func (noAgendas) InvalidateService(context.Context, string, string)    {}

// NewCatalog devuelve los casos de uso del catálogo.
func NewCatalog(store CatalogStore, staff StaffDirectory) *Catalog {
	return &Catalog{store: store, staff: staff, agendas: noAgendas{}}
}

// ObserveAgendas registra a quién avisar cuando el catálogo cambia algo que
// afecta a la disponibilidad.
func (c *Catalog) ObserveAgendas(observer AgendaObserver) {
	c.agendas = observer
}

// ProvisionClinic registra (o actualiza) la proyección local de una clínica.
// Es idempotente, así que Tenancy puede reintentarla.
func (c *Catalog) ProvisionClinic(ctx context.Context, clinicID, timezone string) error {
	clinicID, ok := ids.Normalize(clinicID)
	if !ok {
		return apperr.Invalid("invalid clinic id")
	}

	timezone = strings.TrimSpace(timezone)
	// LoadLocation acepta "" (UTC) y "Local" (la zona del servidor); ninguna
	// de las dos es una zona IANA válida para una clínica.
	if _, err := time.LoadLocation(timezone); err != nil || timezone == "" || timezone == "Local" {
		return apperr.Invalid("invalid time zone")
	}

	return c.store.UpsertClinic(ctx, clinicID, timezone)
}

// DeprovisionClinic elimina la proyección local de una clínica que nunca se
// usó. Es el paso de compensación de la saga con la que Tenancy crea clínicas.
func (c *Catalog) DeprovisionClinic(ctx context.Context, clinicID string) error {
	clinicID, ok := ids.Normalize(clinicID)
	if !ok {
		return apperr.Invalid("invalid clinic id")
	}

	return c.store.DeleteClinic(ctx, clinicID)
}

// CreateService agrega un servicio al catálogo de la clínica del actor (RF3).
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

// ListServices devuelve el catálogo de la clínica del actor.
func (c *Catalog) ListServices(ctx context.Context, actor auth.Actor) ([]MedicalService, error) {
	return c.store.ListServices(ctx, actor.ClinicID)
}

// GetService devuelve un servicio de la clínica del actor.
func (c *Catalog) GetService(ctx context.Context, actor auth.Actor, serviceID string) (MedicalService, error) {
	serviceID, ok := ids.Normalize(serviceID)
	if !ok {
		return MedicalService{}, apperr.Invalid("invalid service id")
	}

	return c.store.GetService(ctx, actor.ClinicID, serviceID)
}

// DeactivateService retira un servicio del catálogo (baja lógica). Las
// asociaciones y las citas que ya existen lo siguen referenciando.
func (c *Catalog) DeactivateService(ctx context.Context, actor auth.Actor, serviceID string) error {
	serviceID, ok := ids.Normalize(serviceID)
	if !ok {
		return apperr.Invalid("invalid service id")
	}

	if err := c.store.DeactivateService(ctx, actor.ClinicID, serviceID); err != nil {
		return err
	}

	c.agendas.InvalidateService(ctx, actor.ClinicID, serviceID)

	return nil
}

// CreatePractitioner registra a un profesional y los servicios que atiende
// (RF4). El usuario debe existir en la clínica del actor con el rol
// practitioner, lo que se comprueba en Tenancy con el token del propio actor.
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
		// Defensa en profundidad: Tenancy ya limita la búsqueda a la clínica.
		return Practitioner{}, apperr.NotFound("user not found")
	}

	if !user.Active || user.Role != string(auth.RolePractitioner) {
		return Practitioner{}, apperr.Conflict("the user is not an active practitioner")
	}

	in.UserSubject = user.Subject

	return c.store.CreatePractitioner(ctx, actor.ClinicID, in, serviceIDs)
}

// ListPractitioners devuelve los profesionales de la clínica del actor.
func (c *Catalog) ListPractitioners(ctx context.Context, actor auth.Actor) ([]Practitioner, error) {
	return c.store.ListPractitioners(ctx, actor.ClinicID)
}

// GetPractitioner devuelve un profesional de la clínica del actor.
func (c *Catalog) GetPractitioner(ctx context.Context, actor auth.Actor, practitionerID string) (Practitioner, error) {
	practitionerID, ok := ids.Normalize(practitionerID)
	if !ok {
		return Practitioner{}, apperr.Invalid("invalid practitioner id")
	}

	return c.store.GetPractitioner(ctx, actor.ClinicID, practitionerID)
}

// ReplacePractitionerServices define los servicios que atiende un profesional.
// Cada servicio debe ser de la clínica, estar activo y requerir la
// especialidad del profesional.
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

	practitioner, err := c.store.ReplacePractitionerServices(ctx, actor.ClinicID, practitionerID, serviceIDs)
	if err != nil {
		return Practitioner{}, err
	}

	c.agendas.InvalidateAgendas(ctx, actor.ClinicID, practitionerID)

	return practitioner, nil
}

// SetPractitionerActive activa o desactiva al profesional que corresponde a un
// usuario de Tenancy. Tenancy lo llama cuando el usuario deja de ser
// profesional (cambio de rol o baja) o vuelve a serlo, para que Agenda no siga
// tratando como profesional activo a quien ya no lo es.
//
// Es idempotente, y un usuario que nunca se registró como profesional no es
// un error: Tenancy no sabe cuáles usuarios tienen profesional en Agenda.
func (c *Catalog) SetPractitionerActive(ctx context.Context, userID string, active bool) error {
	userID, ok := ids.Normalize(userID)
	if !ok {
		return apperr.Invalid("invalid user id")
	}

	clinicID, practitionerID, err := c.store.SetPractitionerActiveByUser(ctx, userID, active)
	if err != nil {
		return err
	}

	if practitionerID != "" {
		c.agendas.InvalidateAgendas(ctx, clinicID, practitionerID)
	}

	return nil
}

func validText(value string, maxLength int) bool {
	return value != "" && utf8.RuneCountInString(value) <= maxLength
}
