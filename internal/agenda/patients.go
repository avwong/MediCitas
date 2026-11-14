package agenda

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/ids"
	"github.com/avwong/MediCitas/internal/platform/outbox"
)

// Límites de longitud
// Coinciden con los VARCHAR de la migración 0004
const (
	maxIdentificationLength = 40
	maxFirstNameLength      = 80
	maxLastNameLength       = 120
	maxPhoneLength          = 30
	maxEmailLength          = 254
	maxAddressLength        = 300

	defaultPatientLimit = 50
	maxPatientLimit     = 200

	// birthDateLayout es el formato de la fecha de nacimiento en la API
	birthDateLayout = "2006-01-02"
)

// minBirthDate coincide con el CHECK de la columna birth_date
var minBirthDate = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

// phonePattern valida el formato del teléfono
// Admite dígitos y espacios
// Admite los símbolos +, - y paréntesis
var phonePattern = regexp.MustCompile(`^[0-9+()\- ]+$`)

// Patient es un paciente de la clínica (RF14)
// Incluye datos de contacto
// Toda lectura o escritura de este tipo se audita (RF18)
type Patient struct {
	ID             string `json:"id"`
	ClinicID       string `json:"clinic_id"`
	Identification string `json:"identification"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	// BirthDate va en formato AAAA-MM-DD
	BirthDate string `json:"birth_date"`
	// Un dato de contacto vacío significa "no registrado"
	Phone     string    `json:"phone"`
	Email     string    `json:"email"`
	Address   string    `json:"address"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PatientSummary es la vista del paciente para los listados
// No lleva datos de contacto a propósito
// Así listar no cuenta como leer contacto
// Y no se genera un evento de auditoría por cada paciente devuelto
type PatientSummary struct {
	ID             string `json:"id"`
	Identification string `json:"identification"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	BirthDate      string `json:"birth_date"`
	Active         bool   `json:"active"`
}

// NewPatient son los datos para registrar un paciente
type NewPatient struct {
	Identification string
	FirstName      string
	LastName       string
	BirthDate      string
	Phone          string
	Email          string
	Address        string
}

// PatientUpdate son los cambios sobre un paciente
// Un campo nil no se modifica
// En los datos de contacto, un texto vacío borra el valor
type PatientUpdate struct {
	Identification *string
	FirstName      *string
	LastName       *string
	BirthDate      *string
	Phone          *string
	Email          *string
	Address        *string
}

// IsEmpty indica si la actualización no trae ningún cambio
func (u PatientUpdate) IsEmpty() bool {
	return u.Identification == nil && u.FirstName == nil && u.LastName == nil &&
		u.BirthDate == nil && u.Phone == nil && u.Email == nil && u.Address == nil
}

// PatientFilter acota el listado de pacientes
type PatientFilter struct {
	// Query busca por prefijo en identificación, nombre o apellido
	Query string
	Limit int
}

// PatientStore persiste pacientes
// Cada método recibe la conexión o transacción en la que debe correr
// Así el servicio escribe la operación y su evento de auditoría de forma atómica
// Solo ve filas de la clínica recibida (I5)
type PatientStore interface {
	Insert(ctx context.Context, db database.DBTX, clinicID string, in NewPatient) (Patient, error)
	Update(ctx context.Context, db database.DBTX, clinicID, patientID string, in PatientUpdate) (Patient, error)
	Get(ctx context.Context, db database.DBTX, clinicID, patientID string) (Patient, error)
	List(ctx context.Context, db database.DBTX, clinicID string, filter PatientFilter) ([]PatientSummary, error)
}

// PatientTxRunner lo implementa *database.TxManager
type PatientTxRunner interface {
	Run(
		ctx context.Context,
		opts database.TxOptions,
		fn func(context.Context, *database.Tx) error,
	) error
}

// PatientAuditWriter lo implementa *outbox.Writer
type PatientAuditWriter interface {
	Append(ctx context.Context, db database.DBTX, event outbox.Event) (outbox.Event, error)
}

// Patients implementa RF14 y sus obligaciones de auditoría de RF18
type Patients struct {
	store PatientStore
	tx    PatientTxRunner
	audit PatientAuditWriter
	// now se inyecta para poder probar la validación de la fecha de nacimiento
	now func() time.Time
}

// NewPatients devuelve los casos de uso de pacientes
func NewPatients(store PatientStore, tx PatientTxRunner, audit PatientAuditWriter) *Patients {
	return &Patients{store: store, tx: tx, audit: audit, now: time.Now}
}

// Create registra un paciente en la clínica del actor (RF14)
// El alta y su evento de auditoría se escriben en la misma transacción
// Si el evento no se puede guardar, el paciente tampoco
func (p *Patients) Create(ctx context.Context, actor auth.Actor, in NewPatient) (Patient, error) {
	in, err := p.normalizeNew(in)
	if err != nil {
		return Patient{}, err
	}

	var patient Patient

	err = p.tx.Run(ctx, database.TxOptions{ClinicID: actor.ClinicID}, func(ctx context.Context, tx *database.Tx) error {
		var err error

		patient, err = p.store.Insert(ctx, tx, actor.ClinicID, in)
		if err != nil {
			return err
		}

		return p.auditContact(ctx, tx, actor, outbox.ActionPatientContactWrite, patient.ID)
	})
	if err != nil {
		return Patient{}, duplicateIdentification(err)
	}

	return patient, nil
}

// Update modifica un paciente de la clínica del actor (RF14)
// Se audita como escritura de contacto aunque el cambio no toque el contacto
// La respuesta devuelve el paciente completo, con sus datos de contacto
func (p *Patients) Update(ctx context.Context, actor auth.Actor, patientID string, in PatientUpdate) (Patient, error) {
	patientID, ok := ids.Normalize(patientID)
	if !ok {
		return Patient{}, apperr.Invalid("invalid patient id")
	}

	in, err := p.normalizeUpdate(in)
	if err != nil {
		return Patient{}, err
	}

	var patient Patient

	err = p.tx.Run(ctx, database.TxOptions{ClinicID: actor.ClinicID}, func(ctx context.Context, tx *database.Tx) error {
		var err error

		patient, err = p.store.Update(ctx, tx, actor.ClinicID, patientID, in)
		if err != nil {
			return err
		}

		return p.auditContact(ctx, tx, actor, outbox.ActionPatientContactWrite, patient.ID)
	})
	if err != nil {
		return Patient{}, duplicateIdentification(err)
	}

	return patient, nil
}

// Get devuelve un paciente de la clínica del actor, con sus datos de contacto
// La lectura y el evento patient_contact.read ocurren en la misma transacción
// Esa transacción es de lectura y escritura (no ReadOnly) porque escribe el evento
// El paciente no se devuelve si su evento de auditoría no se puede guardar
func (p *Patients) Get(ctx context.Context, actor auth.Actor, patientID string) (Patient, error) {
	patientID, ok := ids.Normalize(patientID)
	if !ok {
		return Patient{}, apperr.Invalid("invalid patient id")
	}

	var patient Patient

	err := p.tx.Run(ctx, database.TxOptions{ClinicID: actor.ClinicID}, func(ctx context.Context, tx *database.Tx) error {
		var err error

		patient, err = p.store.Get(ctx, tx, actor.ClinicID, patientID)
		if err != nil {
			return err
		}

		return p.auditContact(ctx, tx, actor, outbox.ActionPatientContactRead, patient.ID)
	})
	if err != nil {
		return Patient{}, err
	}

	return patient, nil
}

// List devuelve los pacientes de la clínica del actor, sin datos de contacto
// Como no lee contacto, no se audita
// La transacción es de solo lectura
func (p *Patients) List(ctx context.Context, actor auth.Actor, filter PatientFilter) ([]PatientSummary, error) {
	filter.Query = strings.TrimSpace(filter.Query)

	if utf8.RuneCountInString(filter.Query) > maxIdentificationLength {
		return nil, apperr.Invalid("query is too long")
	}

	switch {
	case filter.Limit < 0:
		return nil, apperr.Invalid("limit must be positive")
	case filter.Limit == 0:
		filter.Limit = defaultPatientLimit
	case filter.Limit > maxPatientLimit:
		filter.Limit = maxPatientLimit
	}

	var patients []PatientSummary

	err := p.tx.Run(ctx, database.TxOptions{ClinicID: actor.ClinicID, ReadOnly: true}, func(ctx context.Context, tx *database.Tx) error {
		var err error

		patients, err = p.store.List(ctx, tx, actor.ClinicID, filter)

		return err
	})
	if err != nil {
		return nil, err
	}

	return patients, nil
}

// auditContact registra en el outbox de auditoría (RF18) el acceso al contacto del paciente
// El evento lleva solo identificadores
// Nunca lleva los valores de contacto
func (p *Patients) auditContact(
	ctx context.Context,
	db database.DBTX,
	actor auth.Actor,
	action outbox.Action,
	patientID string,
) error {
	_, err := p.audit.Append(ctx, db, outbox.Event{
		ClinicID:      actor.ClinicID,
		ActorSubject:  actor.Subject,
		ActorUsername: actor.Username,
		Action:        action,
		PatientID:     patientID,
		ResourceID:    patientID,
	})

	return err
}

// duplicateIdentification traduce el conflicto genérico de unicidad a un mensaje útil
// Aquí la única restricción única que puede chocar es la identificación dentro de la clínica
func duplicateIdentification(err error) error {
	if errors.Is(err, apperr.ErrConflict) {
		return apperr.Conflict("a patient with that identification already exists")
	}

	return err
}

// normalizeNew limpia y valida los datos de un paciente nuevo
func (p *Patients) normalizeNew(in NewPatient) (NewPatient, error) {
	in.Identification = strings.TrimSpace(in.Identification)
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	in.BirthDate = strings.TrimSpace(in.BirthDate)
	in.Phone = strings.TrimSpace(in.Phone)
	in.Email = strings.TrimSpace(in.Email)
	in.Address = strings.TrimSpace(in.Address)

	switch {
	case !validText(in.Identification, maxIdentificationLength):
		return NewPatient{}, apperr.Invalid("identification is required and must have at most 40 characters")
	case !validText(in.FirstName, maxFirstNameLength):
		return NewPatient{}, apperr.Invalid("first_name is required and must have at most 80 characters")
	case !validText(in.LastName, maxLastNameLength):
		return NewPatient{}, apperr.Invalid("last_name is required and must have at most 120 characters")
	}

	if err := p.validateBirthDate(in.BirthDate); err != nil {
		return NewPatient{}, err
	}

	if err := validateContact(in.Phone, in.Email, in.Address); err != nil {
		return NewPatient{}, err
	}

	return in, nil
}

// normalizeUpdate limpia y valida solo los campos que vienen en la actualización
// Los campos obligatorios no pueden quedar vacíos
// Los de contacto sí pueden, porque un texto vacío los borra
func (p *Patients) normalizeUpdate(in PatientUpdate) (PatientUpdate, error) {
	if in.IsEmpty() {
		return PatientUpdate{}, apperr.Invalid("at least one field is required")
	}

	in.Identification = trimmed(in.Identification)
	in.FirstName = trimmed(in.FirstName)
	in.LastName = trimmed(in.LastName)
	in.BirthDate = trimmed(in.BirthDate)
	in.Phone = trimmed(in.Phone)
	in.Email = trimmed(in.Email)
	in.Address = trimmed(in.Address)

	switch {
	case in.Identification != nil && !validText(*in.Identification, maxIdentificationLength):
		return PatientUpdate{}, apperr.Invalid("identification must not be empty and must have at most 40 characters")
	case in.FirstName != nil && !validText(*in.FirstName, maxFirstNameLength):
		return PatientUpdate{}, apperr.Invalid("first_name must not be empty and must have at most 80 characters")
	case in.LastName != nil && !validText(*in.LastName, maxLastNameLength):
		return PatientUpdate{}, apperr.Invalid("last_name must not be empty and must have at most 120 characters")
	}

	if in.BirthDate != nil {
		if err := p.validateBirthDate(*in.BirthDate); err != nil {
			return PatientUpdate{}, err
		}
	}

	if err := validateContact(valueOrEmpty(in.Phone), valueOrEmpty(in.Email), valueOrEmpty(in.Address)); err != nil {
		return PatientUpdate{}, err
	}

	return in, nil
}

// validateBirthDate exige el formato AAAA-MM-DD
// Rechaza fechas anteriores a 1900
// Rechaza fechas futuras
// La regla de "no futura" vive aquí y no en un CHECK porque depende de la fecha actual
func (p *Patients) validateBirthDate(value string) error {
	birthDate, err := time.Parse(birthDateLayout, value)
	if err != nil {
		return apperr.Invalid("birth_date is required and must have the format YYYY-MM-DD")
	}

	now := p.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	switch {
	case birthDate.Before(minBirthDate):
		return apperr.Invalid("birth_date must not be before 1900-01-01")
	case birthDate.After(today):
		return apperr.Invalid("birth_date must not be in the future")
	}

	return nil
}

// validateContact valida los datos de contacto
// Todos son opcionales
// Un valor vacío se acepta y significa "no registrado"
func validateContact(phone, email, address string) error {
	if phone != "" {
		if utf8.RuneCountInString(phone) > maxPhoneLength || !phonePattern.MatchString(phone) {
			return apperr.Invalid("phone must have at most 30 characters and contain only digits, spaces, +, - and parentheses")
		}
	}

	if email != "" {
		parsed, err := mail.ParseAddress(email)
		// Se compara con el texto original
		// Así se rechazan formas como "Nombre <correo@dominio>", que ParseAddress sí acepta
		if utf8.RuneCountInString(email) > maxEmailLength || err != nil || parsed.Address != email {
			return apperr.Invalid("email must be a valid address of at most 254 characters")
		}
	}

	if utf8.RuneCountInString(address) > maxAddressLength {
		return apperr.Invalid("address must have at most 300 characters")
	}

	return nil
}

// trimmed devuelve una copia recortada del texto
// Conserva el nil
func trimmed(value *string) *string {
	if value == nil {
		return nil
	}

	result := strings.TrimSpace(*value)

	return &result
}

// valueOrEmpty devuelve el texto apuntado
// Si el puntero es nil, devuelve vacío
func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
