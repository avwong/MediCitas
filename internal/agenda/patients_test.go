package agenda

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/outbox"
)

const testPatientID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

// patientReceptionist es el actor de las pruebas. Recepción tiene permiso para
// escribir y leer pacientes.
var patientReceptionist = auth.Actor{
	Subject:  "reception-subject",
	Username: "reception",
	ClinicID: clinicA,
	Role:     auth.RoleReception,
}

// fakePatientStore registra las llamadas que recibe. El SQL real se prueba
// contra la base de datos, no aquí.
type fakePatientStore struct {
	inserted   []NewPatient
	updated    []PatientUpdate
	updatedID  string
	clinicIDs  []string
	listFilter PatientFilter
	listCalls  int
	// err es el error que devuelven todos los métodos.
	err error
}

func (s *fakePatientStore) Insert(_ context.Context, _ database.DBTX, clinicID string, in NewPatient) (Patient, error) {
	s.inserted = append(s.inserted, in)
	s.clinicIDs = append(s.clinicIDs, clinicID)

	if s.err != nil {
		return Patient{}, s.err
	}

	return Patient{ID: testPatientID, ClinicID: clinicID, Identification: in.Identification}, nil
}

func (s *fakePatientStore) Update(_ context.Context, _ database.DBTX, clinicID, patientID string, in PatientUpdate) (Patient, error) {
	s.updated = append(s.updated, in)
	s.updatedID = patientID
	s.clinicIDs = append(s.clinicIDs, clinicID)

	if s.err != nil {
		return Patient{}, s.err
	}

	return Patient{ID: patientID, ClinicID: clinicID}, nil
}

func (s *fakePatientStore) Get(_ context.Context, _ database.DBTX, clinicID, patientID string) (Patient, error) {
	s.clinicIDs = append(s.clinicIDs, clinicID)

	if s.err != nil {
		return Patient{}, s.err
	}

	return Patient{ID: patientID, ClinicID: clinicID, Phone: "2222-3333"}, nil
}

func (s *fakePatientStore) List(_ context.Context, _ database.DBTX, clinicID string, filter PatientFilter) ([]PatientSummary, error) {
	s.listCalls++
	s.listFilter = filter
	s.clinicIDs = append(s.clinicIDs, clinicID)

	return []PatientSummary{}, s.err
}

// fakePatientTx registra las opciones de cada transacción y ejecuta la función
// directamente, sin base de datos.
type fakePatientTx struct {
	options []database.TxOptions
	err     error
}

func (f *fakePatientTx) Run(ctx context.Context, opts database.TxOptions, fn func(context.Context, *database.Tx) error) error {
	f.options = append(f.options, opts)

	if f.err != nil {
		return f.err
	}

	return fn(ctx, nil)
}

// fakePatientAudit guarda los eventos de auditoría que recibe.
type fakePatientAudit struct {
	events []outbox.Event
	err    error
}

func (f *fakePatientAudit) Append(_ context.Context, _ database.DBTX, event outbox.Event) (outbox.Event, error) {
	f.events = append(f.events, event)

	return event, f.err
}

// newTestPatients arma el servicio con sus tres fakes y fija la fecha actual,
// para que las pruebas de fecha de nacimiento no dependan del reloj.
func newTestPatients() (*Patients, *fakePatientStore, *fakePatientTx, *fakePatientAudit) {
	store := &fakePatientStore{}
	tx := &fakePatientTx{}
	audit := &fakePatientAudit{}

	patients := NewPatients(store, tx, audit)
	patients.now = func() time.Time {
		return time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	}

	return patients, store, tx, audit
}

// validNewPatient devuelve un paciente nuevo válido, para que cada caso cambie
// un solo campo.
func validNewPatient() NewPatient {
	return NewPatient{
		Identification: "1-1111-1111",
		FirstName:      "María",
		LastName:       "Pérez",
		BirthDate:      "1990-05-12",
		Phone:          "+506 8888-1111",
		Email:          "maria@correo.com",
		Address:        "Cartago",
	}
}

func patientStrPtr(value string) *string {
	return &value
}

// Cubre RF14 (registrar pacientes con identificación, nombre, fecha de nacimiento y contacto)
//
// Las entradas inválidas se rechazan antes de abrir la transacción: nada llega
// al almacén ni a la auditoría.
func TestCreatePatientValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*NewPatient)
	}{
		{"sin identificación", func(p *NewPatient) { p.Identification = " " }},
		{"identificación muy larga", func(p *NewPatient) { p.Identification = strings.Repeat("1", 41) }},
		{"sin nombre", func(p *NewPatient) { p.FirstName = "" }},
		{"nombre muy largo", func(p *NewPatient) { p.FirstName = strings.Repeat("a", 81) }},
		{"sin apellido", func(p *NewPatient) { p.LastName = "" }},
		{"apellido muy largo", func(p *NewPatient) { p.LastName = strings.Repeat("a", 121) }},
		{"sin fecha de nacimiento", func(p *NewPatient) { p.BirthDate = "" }},
		{"correo inválido", func(p *NewPatient) { p.Email = "no-es-correo" }},
		{"teléfono inválido", func(p *NewPatient) { p.Phone = "abc" }},
		{"dirección muy larga", func(p *NewPatient) { p.Address = strings.Repeat("a", 301) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patients, store, tx, audit := newTestPatients()

			in := validNewPatient()
			tt.change(&in)

			_, err := patients.Create(context.Background(), patientReceptionist, in)
			if !errors.Is(err, apperr.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}

			if len(tx.options) != 0 || len(store.inserted) != 0 || len(audit.events) != 0 {
				t.Error("una entrada inválida no debe abrir transacción, escribir ni auditar")
			}
		})
	}
}

// Cubre RF14 (registrar pacientes)
// Cubre RF18 (auditoría de la escritura de contacto)
// Cubre I5 (la clínica sale del actor)
//
// Un alta válida recorta los textos, usa la clínica del actor y audita la
// escritura.
func TestCreatePatientTrimsAndAuditsTheWrite(t *testing.T) {
	patients, store, tx, audit := newTestPatients()

	in := validNewPatient()
	in.Identification = "  1-1111-1111 "
	in.FirstName = " María  "

	patient, err := patients.Create(context.Background(), patientReceptionist, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.inserted[0].Identification != "1-1111-1111" || store.inserted[0].FirstName != "María" {
		t.Error("el paciente debe guardarse con los textos recortados")
	}

	if store.clinicIDs[0] != clinicA || patient.ClinicID != clinicA {
		t.Error("el paciente debe crearse en la clínica del actor")
	}

	// La transacción lleva la clínica para que RLS pueda usarla.
	if len(tx.options) != 1 || tx.options[0].ClinicID != clinicA || tx.options[0].ReadOnly {
		t.Errorf("opciones de la transacción = %+v, want clínica del actor y lectura-escritura", tx.options)
	}

	if len(audit.events) != 1 {
		t.Fatalf("eventos = %d, want 1", len(audit.events))
	}

	event := audit.events[0]
	if event.Action != outbox.ActionPatientContactWrite ||
		event.ClinicID != clinicA ||
		event.PatientID != testPatientID ||
		event.ResourceID != testPatientID ||
		event.ActorSubject != "reception-subject" ||
		event.ActorUsername != "reception" {
		t.Errorf("evento de auditoría incorrecto: %+v", event)
	}
}

// Cubre RF14 (la identificación no se repite dentro de la clínica)
//
// Una identificación repetida llega del almacén como un conflicto genérico, y
// el servicio la traduce a un mensaje que dice qué pasó.
func TestCreatePatientDuplicateIdentificationIsAConflict(t *testing.T) {
	patients, store, _, _ := newTestPatients()
	store.err = fmt.Errorf("%w: unique_violation", apperr.ErrConflict)

	_, err := patients.Create(context.Background(), patientReceptionist, validNewPatient())
	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}

	message, ok := apperr.PublicMessage(err)
	if !ok || !strings.Contains(message, "identification") {
		t.Errorf("mensaje = %q, want un mensaje que mencione la identificación", message)
	}
}

// Cubre RF18 (la escritura y su evento de auditoría son atómicos)
//
// Si la auditoría falla, el alta falla. Con la transacción real, eso revierte
// el paciente.
func TestCreatePatientFailsWhenTheAuditFails(t *testing.T) {
	patients, _, _, audit := newTestPatients()
	audit.err = errors.New("outbox no disponible")

	patient, err := patients.Create(context.Background(), patientReceptionist, validNewPatient())
	if err == nil {
		t.Fatal("el alta debe fallar si el evento de auditoría no se guarda")
	}

	if patient.ID != "" {
		t.Error("no se debe devolver un paciente cuya auditoría falló")
	}
}

// Cubre RF14 (fecha de nacimiento válida)
//
// Reglas de la fecha de nacimiento. La fecha actual de las pruebas es
// 2026-11-14.
func TestBirthDateRules(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"2026-11-14", true},  // hoy
		{"2026-11-15", false}, // mañana
		{"2999-01-01", false}, // futura
		{"1900-01-01", true},  // límite inferior
		{"1899-12-31", false}, // anterior a 1900
		{"1990-13-45", false}, // fecha imposible
		{"12/05/1990", false}, // formato distinto
		{"", false},           // vacía
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			patients, _, _, _ := newTestPatients()

			in := validNewPatient()
			in.BirthDate = tt.value

			_, err := patients.Create(context.Background(), patientReceptionist, in)

			if tt.valid && err != nil {
				t.Errorf("%q: error = %v, want nil", tt.value, err)
			}

			if !tt.valid && !errors.Is(err, apperr.ErrInvalidInput) {
				t.Errorf("%q: error = %v, want invalid input", tt.value, err)
			}
		})
	}
}

// Cubre RF14 (datos de contacto del paciente)
//
// El contacto es opcional, pero si viene debe tener un formato válido.
func TestContactValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*NewPatient)
		valid  bool
	}{
		{"sin contacto", func(p *NewPatient) { p.Phone, p.Email, p.Address = "", "", "" }, true},
		{"teléfono con símbolos permitidos", func(p *NewPatient) { p.Phone = "(506) 2222-3333" }, true},
		{"teléfono con letras", func(p *NewPatient) { p.Phone = "2222-ABCD" }, false},
		{"teléfono muy largo", func(p *NewPatient) { p.Phone = strings.Repeat("1", 31) }, false},
		{"correo con nombre visible", func(p *NewPatient) { p.Email = "Nombre <a@b.com>" }, false},
		{"correo sin dominio", func(p *NewPatient) { p.Email = "a@" }, false},
		{"correo muy largo", func(p *NewPatient) { p.Email = strings.Repeat("a", 250) + "@b.com" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patients, _, _, _ := newTestPatients()

			in := validNewPatient()
			tt.change(&in)

			_, err := patients.Create(context.Background(), patientReceptionist, in)

			if tt.valid && err != nil {
				t.Errorf("error = %v, want nil", err)
			}

			if !tt.valid && !errors.Is(err, apperr.ErrInvalidInput) {
				t.Errorf("error = %v, want invalid input", err)
			}
		})
	}
}

// Cubre RF14 (actualizar pacientes)
//
// Las actualizaciones inválidas se rechazan antes de la transacción.
func TestUpdatePatientValidation(t *testing.T) {
	tests := []struct {
		name      string
		patientID string
		in        PatientUpdate
	}{
		{"id mal formado", "abc", PatientUpdate{Phone: patientStrPtr("1")}},
		{"sin cambios", testPatientID, PatientUpdate{}},
		{"nombre vacío", testPatientID, PatientUpdate{FirstName: patientStrPtr("  ")}},
		{"apellido vacío", testPatientID, PatientUpdate{LastName: patientStrPtr("")}},
		{"identificación vacía", testPatientID, PatientUpdate{Identification: patientStrPtr("")}},
		{"fecha futura", testPatientID, PatientUpdate{BirthDate: patientStrPtr("2999-01-01")}},
		{"correo inválido", testPatientID, PatientUpdate{Email: patientStrPtr("no-es-correo")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patients, store, tx, audit := newTestPatients()

			_, err := patients.Update(context.Background(), patientReceptionist, tt.patientID, tt.in)
			if !errors.Is(err, apperr.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}

			if len(tx.options) != 0 || len(store.updated) != 0 || len(audit.events) != 0 {
				t.Error("una actualización inválida no debe abrir transacción, escribir ni auditar")
			}
		})
	}
}

// Cubre RF14 (actualizar pacientes)
// Cubre RF18 (auditoría de la escritura de contacto)
//
// Una actualización válida conserva la diferencia entre "no enviado" y
// "enviado vacío". El id se normaliza y el cambio se audita como escritura de
// contacto.
func TestUpdatePatientKeepsOmittedFieldsAndAudits(t *testing.T) {
	patients, store, _, audit := newTestPatients()

	_, err := patients.Update(
		context.Background(),
		patientReceptionist,
		strings.ToUpper(testPatientID),
		PatientUpdate{
			Phone: patientStrPtr("  2222-3333 "),
			Email: patientStrPtr(""),
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.updatedID != testPatientID {
		t.Errorf("id = %q, want el id normalizado en minúsculas", store.updatedID)
	}

	sent := store.updated[0]

	if sent.Identification != nil || sent.FirstName != nil || sent.LastName != nil || sent.BirthDate != nil || sent.Address != nil {
		t.Error("los campos no enviados deben seguir en nil para que no se modifiquen")
	}

	if sent.Phone == nil || *sent.Phone != "2222-3333" {
		t.Error("el teléfono debe llegar recortado")
	}

	if sent.Email == nil || *sent.Email != "" {
		t.Error("un correo enviado vacío debe llegar como texto vacío para que se borre")
	}

	if len(audit.events) != 1 || audit.events[0].Action != outbox.ActionPatientContactWrite {
		t.Errorf("eventos = %+v, want una escritura de contacto", audit.events)
	}
}

// Cubre RF14 (actualizar pacientes)
// Cubre RF18 (una operación fallida no deja evento)
//
// Un paciente que no existe en la clínica no deja ningún evento de auditoría.
func TestUpdatePatientNotFoundWritesNoAudit(t *testing.T) {
	patients, store, _, audit := newTestPatients()
	store.err = fmt.Errorf("%w: no rows", apperr.ErrNotFound)

	_, err := patients.Update(context.Background(), patientReceptionist, testPatientID, PatientUpdate{Phone: patientStrPtr("1")})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}

	if len(audit.events) != 0 {
		t.Error("una actualización fallida no debe auditar")
	}
}

// Cubre RF14 (consultar un paciente)
// Cubre RF18 (auditoría de la lectura de contacto)
// Cubre I5 (la lectura filtra por la clínica del actor)
//
// Leer un paciente audita la lectura de su contacto. La transacción es de
// lectura y escritura porque escribe el evento.
func TestGetPatientAuditsTheRead(t *testing.T) {
	patients, store, tx, audit := newTestPatients()

	patient, err := patients.Get(context.Background(), patientReceptionist, strings.ToUpper(testPatientID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if patient.ID != testPatientID || patient.Phone == "" {
		t.Error("el paciente debe devolverse con su contacto")
	}

	if store.clinicIDs[0] != clinicA {
		t.Error("la lectura debe filtrar por la clínica del actor")
	}

	if len(tx.options) != 1 || tx.options[0].ReadOnly || tx.options[0].ClinicID != clinicA {
		t.Errorf("opciones de la transacción = %+v, want lectura-escritura con la clínica del actor", tx.options)
	}

	if len(audit.events) != 1 || audit.events[0].Action != outbox.ActionPatientContactRead || audit.events[0].PatientID != testPatientID {
		t.Errorf("eventos = %+v, want una lectura de contacto del paciente", audit.events)
	}
}

// Cubre RF18 (la lectura y su evento de auditoría son atómicos)
//
// El contacto no se entrega si la lectura no se pudo auditar.
func TestGetPatientIsNotReturnedWhenTheAuditFails(t *testing.T) {
	patients, _, _, audit := newTestPatients()
	audit.err = errors.New("outbox no disponible")

	patient, err := patients.Get(context.Background(), patientReceptionist, testPatientID)
	if err == nil {
		t.Fatal("la lectura debe fallar si el evento de auditoría no se guarda")
	}

	if patient.ID != "" || patient.Phone != "" {
		t.Error("no se deben devolver datos de contacto sin auditoría")
	}
}

// Cubre RF14 (consultar un paciente)
// Cubre RF18 (una lectura fallida no deja evento)
//
// Un id mal formado se rechaza antes de abrir la transacción, y un paciente
// inexistente no deja evento.
func TestGetPatientRejectsBadIDsAndWritesNoAuditWhenMissing(t *testing.T) {
	patients, store, tx, audit := newTestPatients()

	if _, err := patients.Get(context.Background(), patientReceptionist, "1 OR 1=1"); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("error = %v, want invalid input", err)
	}

	if len(tx.options) != 0 {
		t.Error("un id mal formado no debe abrir transacción")
	}

	store.err = fmt.Errorf("%w: no rows", apperr.ErrNotFound)

	if _, err := patients.Get(context.Background(), patientReceptionist, testPatientID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("error = %v, want not found", err)
	}

	if len(audit.events) != 0 {
		t.Error("leer un paciente inexistente no debe auditar")
	}
}

// Cubre RF14 (consultar pacientes)
// Cubre RF18 (el listado no expone contacto, así que no genera eventos)
//
// El listado no devuelve contacto, así que no audita y usa una transacción de
// solo lectura.
func TestListPatientsDoesNotAudit(t *testing.T) {
	patients, store, tx, audit := newTestPatients()

	if _, err := patients.List(context.Background(), patientReceptionist, PatientFilter{Query: "  mor "}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(audit.events) != 0 {
		t.Error("listar no debe generar eventos de auditoría")
	}

	if len(tx.options) != 1 || !tx.options[0].ReadOnly || tx.options[0].ClinicID != clinicA {
		t.Errorf("opciones de la transacción = %+v, want solo lectura con la clínica del actor", tx.options)
	}

	if store.listFilter.Query != "mor" {
		t.Errorf("búsqueda = %q, want el texto recortado", store.listFilter.Query)
	}
}

// Cubre RF14 (consultar pacientes)
//
// El límite del listado tiene un valor por defecto y un máximo.
func TestListPatientsLimits(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"sin límite usa el valor por defecto", 0, defaultPatientLimit},
		{"límite válido se respeta", 10, 10},
		{"límite excesivo se reduce al máximo", 5000, maxPatientLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patients, store, _, _ := newTestPatients()

			if _, err := patients.List(context.Background(), patientReceptionist, PatientFilter{Limit: tt.limit}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if store.listFilter.Limit != tt.want {
				t.Errorf("límite = %d, want %d", store.listFilter.Limit, tt.want)
			}
		})
	}
}

// Cubre RF14 (consultar pacientes)
//
// Un límite negativo o una búsqueda demasiado larga se rechazan sin consultar
// la base.
func TestListPatientsRejectsInvalidFilters(t *testing.T) {
	patients, store, _, _ := newTestPatients()

	if _, err := patients.List(context.Background(), patientReceptionist, PatientFilter{Limit: -1}); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("límite negativo: error = %v, want invalid input", err)
	}

	if _, err := patients.List(context.Background(), patientReceptionist, PatientFilter{Query: strings.Repeat("a", 41)}); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("búsqueda larga: error = %v, want invalid input", err)
	}

	if store.listCalls != 0 {
		t.Error("un filtro inválido no debe llegar al almacén")
	}
}

// Cubre I5 (ningún dato de una clínica es accesible desde otra)
//
// Con un actor de la clínica B, las cuatro operaciones llegan al almacén con
// la clínica B, y la transacción y el evento de auditoría también. La clínica
// sale siempre del actor autenticado, nunca de la petición.
func TestPatientsAlwaysUseTheActorsClinic(t *testing.T) {
	patients, store, tx, audit := newTestPatients()
	ctx := context.Background()

	actorB := auth.Actor{
		Subject:  "reception-b-subject",
		Username: "reception.b",
		ClinicID: clinicB,
		Role:     auth.RoleReception,
	}

	if _, err := patients.Create(ctx, actorB, validNewPatient()); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := patients.Update(ctx, actorB, testPatientID, PatientUpdate{Phone: patientStrPtr("1")}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := patients.Get(ctx, actorB, testPatientID); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if _, err := patients.List(ctx, actorB, PatientFilter{}); err != nil {
		t.Fatalf("List: %v", err)
	}

	// Cuatro operaciones deben producir cuatro llamadas al almacén.
	if len(store.clinicIDs) != 4 {
		t.Fatalf("llamadas al almacén = %d, want 4", len(store.clinicIDs))
	}

	for _, clinicID := range store.clinicIDs {
		if clinicID != clinicB {
			t.Errorf("el almacén recibió la clínica %q, want la del actor %q", clinicID, clinicB)
		}
	}

	for _, options := range tx.options {
		if options.ClinicID != clinicB {
			t.Errorf("la transacción usó la clínica %q, want %q", options.ClinicID, clinicB)
		}
	}

	// Create, Update y Get auditan, y List no.
	if len(audit.events) != 3 {
		t.Fatalf("eventos = %d, want 3", len(audit.events))
	}

	for _, event := range audit.events {
		if event.ClinicID != clinicB {
			t.Errorf("el evento usó la clínica %q, want %q", event.ClinicID, clinicB)
		}
	}
}

// Cubre I5 (ningún dato de una clínica es accesible desde otra)
//
// Las entradas de pacientes no tienen ningún campo de clínica, así que la
// clínica no se puede forzar desde el cuerpo ni desde los filtros de la
// petición.
func TestPatientInputsHaveNoClinicField(t *testing.T) {
	inputs := []any{NewPatient{}, PatientUpdate{}, PatientFilter{}}

	for _, input := range inputs {
		inputType := reflect.TypeOf(input)

		for i := 0; i < inputType.NumField(); i++ {
			if strings.Contains(strings.ToLower(inputType.Field(i).Name), "clinic") {
				t.Errorf("%s tiene el campo %s: la clínica no debe venir de la entrada", inputType.Name(), inputType.Field(i).Name)
			}
		}
	}
}

// Cubre RF14 (búsqueda de pacientes)
//
// Los comodines de LIKE se tratan como texto literal.
func TestEscapeLike(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"mora", "mora"},
		{"100%", `100\%`},
		{"a_b", `a\_b`},
		{`a\b`, `a\\b`},
		{"%_", `\%\_`},
	}

	for _, tt := range tests {
		if got := escapeLike(tt.in); got != tt.want {
			t.Errorf("escapeLike(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
