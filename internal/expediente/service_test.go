package expediente

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/outbox"
)

const (
	testClinicID      = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testAppointmentID = "11111111-1111-4111-8111-111111111111"
	testPatientID     = "22222222-2222-4222-8222-222222222222"
	testNoteID        = "33333333-3333-4333-8333-333333333333"
)

type fakeAppointmentVerifier struct {
	result AppointmentContext
	err    error

	token string
	id    string
	calls int
}

func (f *fakeAppointmentVerifier) ClinicalNoteContext(
	_ context.Context,
	token string,
	appointmentID string,
) (AppointmentContext, error) {
	f.calls++
	f.token = token
	f.id = appointmentID
	return f.result, f.err
}

type fakeNoteStore struct {
	insertResult ClinicalNote
	insertErr    error
	insertInput  NewClinicalNote
	insertCalls  int

	getResult  ClinicalNote
	getErr     error
	getClinic  string
	getID      string
	getSubject string
	getCalls   int
}

func (f *fakeNoteStore) Insert(
	_ context.Context,
	_ database.DBTX,
	in NewClinicalNote,
) (ClinicalNote, error) {
	f.insertCalls++
	f.insertInput = in
	return f.insertResult, f.insertErr
}

func (f *fakeNoteStore) GetOwned(
	_ context.Context,
	_ database.DBTX,
	clinicID string,
	noteID string,
	subject string,
) (ClinicalNote, error) {
	f.getCalls++
	f.getClinic = clinicID
	f.getID = noteID
	f.getSubject = subject
	return f.getResult, f.getErr
}

type fakeTxRunner struct {
	options []database.TxOptions
	err     error
}

func (f *fakeTxRunner) Run(
	ctx context.Context,
	opts database.TxOptions,
	fn func(context.Context, *database.Tx) error,
) error {
	f.options = append(f.options, opts)

	if f.err != nil {
		return f.err
	}

	return fn(ctx, nil)
}

type fakeAuditWriter struct {
	events []outbox.Event
	err    error
}

func (f *fakeAuditWriter) Append(
	_ context.Context,
	_ database.DBTX,
	event outbox.Event,
) (outbox.Event, error) {
	f.events = append(f.events, event)
	return event, f.err
}

func practitionerActor() auth.Actor {
	return auth.Actor{
		Subject:  "practitioner-subject",
		Username: "practitioner",
		ClinicID: testClinicID,
		Role:     auth.RolePractitioner,
		Token:    "raw-user-token",
	}
}

func attendedAppointment() AppointmentContext {
	return AppointmentContext{
		AppointmentID:       testAppointmentID,
		ClinicID:            testClinicID,
		PatientID:           testPatientID,
		PractitionerSubject: "practitioner-subject",
		Status:              AppointmentStatusAttended,
	}
}

func clinicalNote() ClinicalNote {
	return ClinicalNote{
		ID:                  testNoteID,
		ClinicID:            testClinicID,
		AppointmentID:       testAppointmentID,
		PatientID:           testPatientID,
		PractitionerSubject: "practitioner-subject",
		Content:             "Patient progressing normally.",
		CreatedAt:           time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC),
	}
}

func TestCreateClinicalNoteUsesTrustedAppointmentContextAndAuditsWrite(
	t *testing.T,
) {
	verifier := &fakeAppointmentVerifier{
		result: attendedAppointment(),
	}
	store := &fakeNoteStore{
		insertResult: clinicalNote(),
	}
	tx := &fakeTxRunner{}
	audit := &fakeAuditWriter{}

	service := NewService(store, verifier, tx, audit)
	actor := practitionerActor()

	got, err := service.Create(
		context.Background(),
		actor,
		CreateNoteInput{
			AppointmentID: testAppointmentID,
			Content:       "Patient progressing normally.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != testNoteID {
		t.Fatalf("note id = %q, want %q", got.ID, testNoteID)
	}

	if verifier.token != actor.Token {
		t.Fatalf("relayed token = %q, want actor token", verifier.token)
	}

	if store.insertInput.ClinicID != actor.ClinicID {
		t.Fatalf(
			"stored clinic = %q, want %q",
			store.insertInput.ClinicID,
			actor.ClinicID,
		)
	}

	if store.insertInput.PatientID != testPatientID {
		t.Fatalf(
			"stored patient = %q, want %q",
			store.insertInput.PatientID,
			testPatientID,
		)
	}

	if store.insertInput.PractitionerSubject != actor.Subject {
		t.Fatalf(
			"stored practitioner = %q, want %q",
			store.insertInput.PractitionerSubject,
			actor.Subject,
		)
	}

	if len(tx.options) != 1 ||
		tx.options[0].ClinicID != actor.ClinicID ||
		tx.options[0].ReadOnly {
		t.Fatalf("unexpected transaction options: %+v", tx.options)
	}

	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}

	event := audit.events[0]

	if event.Action != outbox.ActionClinicalNoteWrite {
		t.Fatalf(
			"audit action = %q, want %q",
			event.Action,
			outbox.ActionClinicalNoteWrite,
		)
	}

	if event.PatientID != testPatientID ||
		event.ResourceID != testNoteID {
		t.Fatalf("unexpected audit event: %+v", event)
	}
}

func TestCreateRejectsAppointmentThatIsNotAttended(t *testing.T) {
	appointment := attendedAppointment()
	appointment.Status = "reserved"

	verifier := &fakeAppointmentVerifier{result: appointment}
	store := &fakeNoteStore{}

	service := NewService(
		store,
		verifier,
		&fakeTxRunner{},
		&fakeAuditWriter{},
	)

	_, err := service.Create(
		context.Background(),
		practitionerActor(),
		CreateNoteInput{
			AppointmentID: testAppointmentID,
			Content:       "note",
		},
	)

	if !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}

	if store.insertCalls != 0 {
		t.Fatal("note must not be inserted before appointment is attended")
	}
}

func TestCreateHidesAnotherPractitionersAppointment(t *testing.T) {
	appointment := attendedAppointment()
	appointment.PractitionerSubject = "different-practitioner"

	service := NewService(
		&fakeNoteStore{},
		&fakeAppointmentVerifier{result: appointment},
		&fakeTxRunner{},
		&fakeAuditWriter{},
	)

	_, err := service.Create(
		context.Background(),
		practitionerActor(),
		CreateNoteInput{
			AppointmentID: testAppointmentID,
			Content:       "note",
		},
	)

	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestCreateRejectsNonPractitionerBeforeCallingAgenda(t *testing.T) {
	verifier := &fakeAppointmentVerifier{
		result: attendedAppointment(),
	}

	service := NewService(
		&fakeNoteStore{},
		verifier,
		&fakeTxRunner{},
		&fakeAuditWriter{},
	)

	actor := practitionerActor()
	actor.Role = auth.RoleReception

	_, err := service.Create(
		context.Background(),
		actor,
		CreateNoteInput{
			AppointmentID: testAppointmentID,
			Content:       "note",
		},
	)

	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}

	if verifier.calls != 0 {
		t.Fatal("Agenda must not be called for unauthorized actor")
	}
}

func TestGetScopesByClinicAndPractitionerAndAuditsRead(t *testing.T) {
	store := &fakeNoteStore{
		getResult: clinicalNote(),
	}
	tx := &fakeTxRunner{}
	audit := &fakeAuditWriter{}

	service := NewService(
		store,
		&fakeAppointmentVerifier{},
		tx,
		audit,
	)

	actor := practitionerActor()

	got, err := service.Get(
		context.Background(),
		actor,
		testNoteID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != testNoteID {
		t.Fatalf("note id = %q, want %q", got.ID, testNoteID)
	}

	if store.getClinic != actor.ClinicID ||
		store.getSubject != actor.Subject ||
		store.getID != testNoteID {
		t.Fatalf(
			"unexpected note scope: clinic=%q subject=%q id=%q",
			store.getClinic,
			store.getSubject,
			store.getID,
		)
	}

	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}

	event := audit.events[0]

	if event.Action != outbox.ActionClinicalNoteRead {
		t.Fatalf(
			"audit action = %q, want %q",
			event.Action,
			outbox.ActionClinicalNoteRead,
		)
	}

	if len(tx.options) != 1 || tx.options[0].ReadOnly {
		t.Fatal("audited read must use a read-write transaction")
	}
}

func TestCreateRejectsInvalidAppointmentIDBeforeCallingAgenda(t *testing.T) {
	verifier := &fakeAppointmentVerifier{}

	service := NewService(
		&fakeNoteStore{},
		verifier,
		&fakeTxRunner{},
		&fakeAuditWriter{},
	)

	_, err := service.Create(
		context.Background(),
		practitionerActor(),
		CreateNoteInput{
			AppointmentID: "not-a-uuid",
			Content:       "note",
		},
	)

	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("error = %v, want invalid input", err)
	}

	if verifier.calls != 0 {
		t.Fatal("Agenda must not be called for malformed appointment id")
	}
}
