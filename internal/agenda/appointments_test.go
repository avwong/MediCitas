package agenda

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
)

const (
	testPatient = "99999999-0000-4000-8000-000000000001"
	// Los ids de los profesionales están en este orden a propósito: la cita
	// de la prueba de candados es de drB y se mueve a drA.
	drA      = "c1c1c1c1-0000-4000-8000-00000000000a"
	drB      = "c1c1c1c1-0000-4000-8000-00000000000b"
	checkup  = "d1d1d1d1-0000-4000-8000-000000000001"
	existing = "eeeeeeee-0000-4000-8000-000000000001"
)

var schedulingReception = auth.Actor{Subject: "reception-subject", ClinicID: cacheClinic, Role: auth.RoleReception}

// fakeSchedulingStore es un almacén en memoria con lo mínimo para ejercitar
// las reglas del servicio. El SQL real se prueba contra la base, en
// cmd/citascheck.
type fakeSchedulingStore struct {
	SchedulingStore

	practitioners map[string]practitionerRef
	offers        map[string]bool
	serviceActive bool
	patientActive bool
	schedule      []ScheduleBlock
	blocks        []AgendaBlock
	booked        []Appointment
	appointment   Appointment

	locks      []string
	inserted   []newAppointment
	closed     []string
	scheduleQ  int
	insertErr  error
	patientErr error
}

func newFakeSchedulingStore() *fakeSchedulingStore {
	return &fakeSchedulingStore{
		practitioners: map[string]practitionerRef{
			drA: {ID: drA, DisplayName: "Dra. A", UserSubject: "subject-a", Active: true},
			drB: {ID: drB, DisplayName: "Dr. B", UserSubject: "subject-b", Active: true},
		},
		offers:        map[string]bool{drA: true, drB: true},
		serviceActive: true,
		patientActive: true,
		schedule:      splitDay,
	}
}

func (s *fakeSchedulingStore) ClinicTimezone(context.Context, database.DBTX, string) (string, error) {
	return "America/Costa_Rica", nil
}

func (s *fakeSchedulingStore) Practitioner(_ context.Context, _ database.DBTX, _, id string, lock bool) (practitionerRef, error) {
	ref, ok := s.practitioners[id]
	if !ok {
		return practitionerRef{}, apperr.ErrNotFound
	}

	if lock {
		s.locks = append(s.locks, id)
	}

	return ref, nil
}

func (s *fakeSchedulingStore) Service(_ context.Context, _ database.DBTX, _, id string) (serviceRef, error) {
	return serviceRef{ID: id, Duration: 30 * time.Minute, Active: s.serviceActive}, nil
}

func (s *fakeSchedulingStore) PractitionerOffers(_ context.Context, _ database.DBTX, _, practitionerID, _ string) (bool, error) {
	return s.offers[practitionerID], nil
}

func (s *fakeSchedulingStore) PatientActive(context.Context, database.DBTX, string, string) (bool, error) {
	return s.patientActive, s.patientErr
}

func (s *fakeSchedulingStore) Schedule(context.Context, database.DBTX, string, string) ([]ScheduleBlock, error) {
	s.scheduleQ++
	return s.schedule, nil
}

func (s *fakeSchedulingStore) BlocksInRange(_ context.Context, _ database.DBTX, _, _ string, from, to time.Time) ([]AgendaBlock, error) {
	var result []AgendaBlock

	for _, block := range s.blocks {
		if block.StartsAt.Before(to) && from.Before(block.EndsAt) {
			result = append(result, block)
		}
	}

	return result, nil
}

func (s *fakeSchedulingStore) BookedInRange(context.Context, database.DBTX, string, string, time.Time, time.Time) ([]Appointment, error) {
	return s.booked, nil
}

func (s *fakeSchedulingStore) InsertAppointment(_ context.Context, _ database.DBTX, clinicID string, in newAppointment) (Appointment, error) {
	if s.insertErr != nil {
		return Appointment{}, s.insertErr
	}

	s.inserted = append(s.inserted, in)

	return Appointment{
		ID:                "new-appointment",
		ClinicID:          clinicID,
		PatientID:         in.PatientID,
		PractitionerID:    in.PractitionerID,
		ServiceID:         in.ServiceID,
		StartsAt:          in.StartsAt,
		Status:            StatusBooked,
		RescheduledFromID: in.RescheduledFromID,
	}, nil
}

func (s *fakeSchedulingStore) Appointment(context.Context, database.DBTX, string, string, bool) (Appointment, error) {
	if s.appointment.ID == "" {
		return Appointment{}, apperr.ErrNotFound
	}

	return s.appointment, nil
}

func (s *fakeSchedulingStore) CloseAppointment(
	_ context.Context, _ database.DBTX, _, id, status, _ string, _ *string, _ bool,
) (Appointment, bool, error) {
	if s.appointment.Status != StatusBooked {
		return Appointment{}, false, nil
	}

	s.closed = append(s.closed, id+"->"+status)
	s.appointment.Status = status

	return s.appointment, true, nil
}

// fakeSchedulingTx ejecuta la función directamente, sin base de datos.
type fakeSchedulingTx struct {
	options []database.TxOptions
}

func (f *fakeSchedulingTx) Run(ctx context.Context, opts database.TxOptions, fn func(context.Context, *database.Tx) error) error {
	f.options = append(f.options, opts)

	return fn(ctx, &database.Tx{})
}

// countingCache envuelve la caché real sobre un Redis en memoria y cuenta las
// invalidaciones.
type countingCache struct {
	*RedisAvailabilityCache
	invalidated []string
}

func (c *countingCache) Invalidate(ctx context.Context, clinicID, practitionerID string) {
	c.invalidated = append(c.invalidated, practitionerID)
	c.RedisAvailabilityCache.Invalidate(ctx, clinicID, practitionerID)
}

func newTestScheduling() (*Scheduling, *fakeSchedulingStore, *countingCache, *fakeSchedulingTx) {
	store := newFakeSchedulingStore()
	tx := &fakeSchedulingTx{}

	redisCache, _ := newTestCache()
	cache := &countingCache{RedisAvailabilityCache: redisCache}

	scheduling := NewScheduling(store, tx, cache)
	// "Ahora" es el domingo anterior al lunes de las pruebas.
	scheduling.now = func() time.Time { return at(12, 0).AddDate(0, 0, -1) }

	return scheduling, store, cache, tx
}

func booking(practitionerID string, startsAt time.Time) NewBooking {
	return NewBooking{
		PatientID:      testPatient,
		PractitionerID: practitionerID,
		ServiceID:      checkup,
		StartsAt:       startsAt.Format(time.RFC3339),
	}
}

// Cubre RF9 (reservar)
// Cubre I5 (la clínica sale del actor)
func TestBookGrantsAValidSlot(t *testing.T) {
	scheduling, store, cache, tx := newTestScheduling()

	appointment, err := scheduling.Book(context.Background(), schedulingReception, booking(drA, at(9, 0)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if appointment.ClinicID != cacheClinic || appointment.Status != StatusBooked {
		t.Errorf("unexpected appointment: %+v", appointment)
	}

	if len(store.inserted) != 1 || store.inserted[0].CreatedBy != "reception-subject" {
		t.Fatalf("inserted = %+v", store.inserted)
	}

	// La fila del profesional se bloquea antes de validar e insertar.
	if len(store.locks) != 1 || store.locks[0] != drA {
		t.Errorf("locks = %v, want the practitioner row", store.locks)
	}

	if len(tx.options) != 1 || tx.options[0].ClinicID != cacheClinic {
		t.Errorf("the transaction must carry the actor's clinic: %+v", tx.options)
	}

	// Una invalidación antes del commit; la de después la dispara la
	// transacción real.
	if len(cache.invalidated) != 1 || cache.invalidated[0] != drA {
		t.Errorf("invalidated = %v", cache.invalidated)
	}
}

// Cubre RF9 (la solicitud inválida se distingue del espacio no disponible)
//
// Las entradas mal formadas se rechazan antes de abrir la transacción.
func TestBookRejectsInvalidInput(t *testing.T) {
	scheduling, store, _, tx := newTestScheduling()

	invalid := []NewBooking{
		{PatientID: "abc", PractitionerID: drA, ServiceID: checkup, StartsAt: at(9, 0).Format(time.RFC3339)},
		{PatientID: testPatient, PractitionerID: "", ServiceID: checkup, StartsAt: at(9, 0).Format(time.RFC3339)},
		{PatientID: testPatient, PractitionerID: drA, ServiceID: "1", StartsAt: at(9, 0).Format(time.RFC3339)},
		{PatientID: testPatient, PractitionerID: drA, ServiceID: checkup, StartsAt: "2030-03-04 09:00"},
		{PatientID: testPatient, PractitionerID: drA, ServiceID: checkup, StartsAt: ""},
		// En el pasado respecto del "ahora" de la prueba.
		booking(drA, at(9, 0).AddDate(0, 0, -7)),
	}

	for _, in := range invalid {
		if _, err := scheduling.Book(context.Background(), schedulingReception, in); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%+v: error = %v, want invalid input", in, err)
		}
	}

	if len(tx.options) != 0 || len(store.inserted) != 0 {
		t.Fatal("invalid input must not open a transaction")
	}
}

// Cubre I3 (dentro del horario y fuera de los bloqueos)
// Cubre RF9 (el espacio no disponible es un conflicto)
func TestBookRejectsUnavailableSlots(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*fakeSchedulingStore)
		start   time.Time
		want    error
	}{
		{"fuera del horario", func(*fakeSchedulingStore) {}, at(12, 15), apperr.ErrConflict},
		{"termina después de cerrar", func(*fakeSchedulingStore) {}, at(16, 45), apperr.ErrConflict},
		{"dentro de un bloqueo", func(s *fakeSchedulingStore) {
			s.blocks = []AgendaBlock{{StartsAt: at(9, 0), EndsAt: at(10, 0)}}
		}, at(9, 30), apperr.ErrConflict},
		{"toca el inicio de un bloqueo", func(s *fakeSchedulingStore) {
			s.blocks = []AgendaBlock{{StartsAt: at(9, 15), EndsAt: at(10, 0)}}
		}, at(9, 0), apperr.ErrConflict},
		{"profesional inactivo", func(s *fakeSchedulingStore) {
			ref := s.practitioners[drA]
			ref.Active = false
			s.practitioners[drA] = ref
		}, at(9, 0), apperr.ErrConflict},
		{"el profesional no atiende el servicio", func(s *fakeSchedulingStore) { s.offers[drA] = false }, at(9, 0), apperr.ErrConflict},
		{"servicio retirado", func(s *fakeSchedulingStore) { s.serviceActive = false }, at(9, 0), apperr.ErrConflict},
		{"paciente inactivo", func(s *fakeSchedulingStore) { s.patientActive = false }, at(9, 0), apperr.ErrConflict},
		{"paciente inexistente", func(s *fakeSchedulingStore) { s.patientErr = apperr.ErrNotFound }, at(9, 0), apperr.ErrNotFound},
		{"espacio ya tomado", func(s *fakeSchedulingStore) { s.insertErr = apperr.Conflict("slot already taken") }, at(9, 0), apperr.ErrConflict},
	}

	for _, tc := range cases {
		scheduling, store, cache, _ := newTestScheduling()
		tc.prepare(store)

		_, err := scheduling.Book(context.Background(), schedulingReception, booking(drA, tc.start))
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}

		if len(store.inserted) != 0 {
			t.Errorf("%s: the appointment must not be inserted", tc.name)
		}

		if len(cache.invalidated) != 0 {
			t.Errorf("%s: a rejected booking must not invalidate the cache", tc.name)
		}
	}

	// Un bloqueo que termina justo donde empieza la cita no la afecta.
	scheduling, store, _, _ := newTestScheduling()
	store.blocks = []AgendaBlock{{StartsAt: at(8, 0), EndsAt: at(9, 0)}}

	if _, err := scheduling.Book(context.Background(), schedulingReception, booking(drA, at(9, 0))); err != nil {
		t.Errorf("a slot right after a block must be allowed: %v", err)
	}
}

// Cubre RF10 (reprogramar en una sola transacción)
//
// Al cambiar de profesional se bloquean las dos filas en orden de id, sin
// importar cuál es el origen y cuál el destino: así dos reprogramaciones
// cruzadas no pueden formar un deadlock. Además, la cita anterior se cierra
// antes de insertar la nueva.
func TestRescheduleLocksPractitionersInIDOrder(t *testing.T) {
	scheduling, store, cache, tx := newTestScheduling()
	store.appointment = Appointment{
		ID:             existing,
		PatientID:      testPatient,
		PractitionerID: drB,
		ServiceID:      checkup,
		StartsAt:       at(9, 0),
		Status:         StatusBooked,
	}

	created, err := scheduling.Reschedule(context.Background(), schedulingReception, existing, Rescheduling{
		StartsAt:       at(10, 0).Format(time.RFC3339),
		PractitionerID: drA,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.locks) != 2 || store.locks[0] != drA || store.locks[1] != drB {
		t.Errorf("locks = %v, want [%s %s]", store.locks, drA, drB)
	}

	if len(store.closed) != 1 || store.closed[0] != existing+"->"+StatusRescheduled {
		t.Errorf("closed = %v", store.closed)
	}

	if created.RescheduledFromID == nil || *created.RescheduledFromID != existing || created.PractitionerID != drA {
		t.Errorf("the new appointment must point to the previous one: %+v", created)
	}

	if len(tx.options) != 1 {
		t.Errorf("rescheduling must use a single transaction, used %d", len(tx.options))
	}

	// Cambian las dos agendas.
	if len(cache.invalidated) != 2 {
		t.Errorf("invalidated = %v, want both practitioners", cache.invalidated)
	}
}

// Cubre RF10 y RF13 (solo se reprograma una cita vigente)
func TestRescheduleRejections(t *testing.T) {
	for _, status := range []string{StatusAttended, StatusNoShow, StatusCancelled, StatusRescheduled} {
		scheduling, store, _, _ := newTestScheduling()
		store.appointment = Appointment{ID: existing, PractitionerID: drA, ServiceID: checkup, StartsAt: at(9, 0), Status: status}

		_, err := scheduling.Reschedule(context.Background(), schedulingReception, existing, Rescheduling{StartsAt: at(10, 0).Format(time.RFC3339)})
		if !errors.Is(err, apperr.ErrConflict) {
			t.Errorf("%s: error = %v, want conflict", status, err)
		}

		if len(store.inserted) != 0 || len(store.closed) != 0 {
			t.Errorf("%s: nothing must change", status)
		}
	}

	scheduling, store, _, _ := newTestScheduling()
	store.appointment = Appointment{ID: existing, PractitionerID: drA, ServiceID: checkup, StartsAt: at(9, 0), Status: StatusBooked}

	// Al mismo espacio que ya tiene.
	if _, err := scheduling.Reschedule(context.Background(), schedulingReception, existing, Rescheduling{StartsAt: at(9, 0).Format(time.RFC3339)}); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("same slot: error = %v, want invalid input", err)
	}

	// Una cita que no existe en la clínica.
	scheduling, _, _, _ = newTestScheduling()
	if _, err := scheduling.Reschedule(context.Background(), schedulingReception, existing, Rescheduling{StartsAt: at(10, 0).Format(time.RFC3339)}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown appointment: error = %v, want not found", err)
	}
}

// Cubre RF11 (cancelar exige motivo) y RF12 (el desenlace tiene dos valores)
func TestCancelAndOutcomeValidation(t *testing.T) {
	scheduling, store, _, tx := newTestScheduling()
	store.appointment = Appointment{ID: existing, PractitionerID: drA, Status: StatusBooked}

	for _, reason := range []string{"", "   "} {
		if _, err := scheduling.Cancel(context.Background(), schedulingReception, existing, reason); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("reason %q: error = %v, want invalid input", reason, err)
		}
	}

	for _, outcome := range []string{"", "cancelled", "booked", "rescheduled", "done"} {
		if _, err := scheduling.RecordOutcome(context.Background(), schedulingReception, existing, outcome); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("outcome %q: error = %v, want invalid input", outcome, err)
		}
	}

	if len(tx.options) != 0 {
		t.Fatal("invalid input must not open a transaction")
	}

	cancelled, err := scheduling.Cancel(context.Background(), schedulingReception, existing, "  El paciente avisó  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cancelled.Status != StatusCancelled {
		t.Errorf("status = %s", cancelled.Status)
	}

	// RF13: ya es un estado final.
	if _, err := scheduling.Cancel(context.Background(), schedulingReception, existing, "Otra vez"); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("second cancel: error = %v, want conflict", err)
	}
}

// Cubre RF5 y RF6 (el profesional solo administra su propia agenda)
func TestOwnAgenda(t *testing.T) {
	practitioner := practitionerRef{ID: drA, UserSubject: "subject-a"}

	cases := []struct {
		actor auth.Actor
		ok    bool
	}{
		{auth.Actor{Subject: "subject-a", Role: auth.RolePractitioner}, true},
		{auth.Actor{Subject: "subject-b", Role: auth.RolePractitioner}, false},
		{auth.Actor{Subject: "admin", Role: auth.RoleClinicAdmin}, true},
	}

	for _, tc := range cases {
		err := ownAgenda(tc.actor, practitioner)
		if tc.ok && err != nil {
			t.Errorf("%+v: unexpected error %v", tc.actor, err)
		}

		if !tc.ok && !errors.Is(err, apperr.ErrForbidden) {
			t.Errorf("%+v: error = %v, want forbidden", tc.actor, err)
		}
	}
}

// Cubre RF7 e I8 (la disponibilidad se sirve de la caché hasta que la agenda
// cambia)
func TestAvailabilityUsesTheCacheUntilTheAgendaChanges(t *testing.T) {
	scheduling, store, _, _ := newTestScheduling()
	ctx := context.Background()

	first, cached, err := scheduling.Availability(ctx, schedulingReception, drA, checkup, "2030-03-04")
	if err != nil || cached {
		t.Fatalf("first read: cached = %v, err = %v", cached, err)
	}

	queries := store.scheduleQ

	second, cached, err := scheduling.Availability(ctx, schedulingReception, drA, checkup, "2030-03-04")
	if err != nil || !cached {
		t.Fatalf("second read: cached = %v, err = %v", cached, err)
	}

	if store.scheduleQ != queries {
		t.Error("a cache hit must not read the agenda from the store")
	}

	if len(first) != len(second) || len(first) != 16 {
		t.Fatalf("got %d and %d slots, want 16", len(first), len(second))
	}

	// Se reserva el espacio de las 09:00 y el almacén empieza a devolverlo
	// como cita vigente.
	if _, err := scheduling.Book(ctx, schedulingReception, booking(drA, at(9, 0))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store.booked = []Appointment{{StartsAt: at(9, 0), EndsAt: at(9, 30)}}

	third, cached, err := scheduling.Availability(ctx, schedulingReception, drA, checkup, "2030-03-04")
	if err != nil || cached {
		t.Fatalf("read after booking: cached = %v, err = %v", cached, err)
	}

	if len(third) != 15 {
		t.Fatalf("after booking there must be 15 slots, got %d", len(third))
	}

	for _, slot := range third {
		if slot.StartsAt.Equal(at(9, 0)) {
			t.Fatal("the booked slot is still offered")
		}
	}

	// La agenda de otro profesional no se invalidó.
	if _, _, err := scheduling.Availability(ctx, schedulingReception, drB, checkup, "2030-03-04"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, cached, _ := scheduling.Availability(ctx, schedulingReception, drB, checkup, "2030-03-04"); !cached {
		t.Error("another practitioner's agenda must still be cached")
	}
}

// Un espacio que ya empezó no se ofrece, aunque la entrada de la caché se
// haya calculado antes.
func TestAvailabilityHidesSlotsThatAlreadyStarted(t *testing.T) {
	scheduling, _, _, _ := newTestScheduling()
	ctx := context.Background()

	if _, _, err := scheduling.Availability(ctx, schedulingReception, drA, checkup, "2030-03-04"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Pasa el tiempo: ahora son las 10:10 de ese mismo lunes.
	scheduling.now = func() time.Time { return at(10, 10) }

	slots, cached, err := scheduling.Availability(ctx, schedulingReception, drA, checkup, "2030-03-04")
	if err != nil || !cached {
		t.Fatalf("cached = %v, err = %v", cached, err)
	}

	if len(slots) == 0 || slots[0].StartsAt.Before(at(10, 10)) {
		t.Fatalf("the first slot offered is %v", slots[0].StartsAt)
	}

	if !slots[0].StartsAt.Equal(at(10, 30)) {
		t.Errorf("the first slot must be 10:30, got %v", slots[0].StartsAt)
	}
}

// Un profesional inactivo o un servicio que no atiende no tienen espacios.
func TestAvailabilityIsEmptyWhenBookingWouldBeRejected(t *testing.T) {
	scheduling, store, _, _ := newTestScheduling()
	store.offers[drA] = false

	slots, _, err := scheduling.Availability(context.Background(), schedulingReception, drA, checkup, "2030-03-04")
	if err != nil || len(slots) != 0 {
		t.Fatalf("slots = %v, err = %v", slots, err)
	}

	for _, date := range []string{"", "04/03/2030", "2030-13-40"} {
		if _, _, err := scheduling.Availability(context.Background(), schedulingReception, drA, checkup, date); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("date %q: error = %v, want invalid input", date, err)
		}
	}
}
