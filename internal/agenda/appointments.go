package agenda

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/ids"
)

const (
	maxBlockReasonLength        = 200
	maxCancellationReasonLength = 300
	// maxAvailabilityDays acota el rango de fechas de RF8.
	maxAvailabilityDays = 31
	// conflictHorizon es hasta dónde se buscan citas en conflicto al cambiar
	// un horario, y bloqueos cuando el listado no trae fecha final.
	conflictHorizon = 5 * 365 * 24 * time.Hour
)

// SchedulingTxRunner lo implementa *database.TxManager.
type SchedulingTxRunner interface {
	Run(
		ctx context.Context,
		opts database.TxOptions,
		fn func(context.Context, *database.Tx) error,
	) error
}

// Scheduling implementa los horarios y bloqueos (RF5, RF6), la disponibilidad
// (RF7, RF8) y el ciclo de vida de la cita (RF9 a RF13).
//
// Todas las transacciones corren en READ COMMITTED. Que dos citas no se
// solapen (I1, I2) no depende de lo que la transacción haya leído, sino de
// las restricciones de exclusión de la tabla, que se comprueban al escribir.
// Lo que sí depende de una lectura previa, que la cita caiga dentro del
// horario y fuera de los bloqueos (I3), se protege bloqueando la fila del
// profesional: reservar, reprogramar, cambiar el horario y crear un bloqueo
// toman ese mismo candado, así que ocurren uno después del otro.
type Scheduling struct {
	store SchedulingStore
	tx    SchedulingTxRunner
	// db es el pool, para las lecturas que no necesitan transacción.
	db    database.DBTX
	cache AvailabilityCache
	// now se inyecta para poder probar las reglas que dependen de la hora.
	now func() time.Time
}

// NewScheduling devuelve los casos de uso de la agenda.
func NewScheduling(store SchedulingStore, tx SchedulingTxRunner, db database.DBTX, cache AvailabilityCache) *Scheduling {
	return &Scheduling{store: store, tx: tx, db: db, cache: cache, now: time.Now}
}

// txOptions son las opciones de toda transacción de escritura de la agenda.
func txOptions(actor auth.Actor) database.TxOptions {
	return database.TxOptions{IsoLevel: pgx.ReadCommitted, ClinicID: actor.ClinicID}
}

// invalidate sube la versión de la agenda del profesional dos veces: ahora,
// antes del commit, y otra vez después.
//
// La primera hace que ninguna consulta vuelva a acertar con una entrada
// calculada antes de este cambio. Lo que se calcule entre las dos todavía
// puede haber leído la base sin el cambio, y la segunda lo deja huérfano. Si
// la transacción se revierte, la única consecuencia es un fallo de caché.
func (s *Scheduling) invalidate(ctx context.Context, tx *database.Tx, clinicID string, practitionerIDs ...string) {
	for _, practitionerID := range practitionerIDs {
		s.cache.Invalidate(ctx, clinicID, practitionerID)
	}

	tx.AfterCommit(func(ctx context.Context) {
		for _, practitionerID := range practitionerIDs {
			s.cache.Invalidate(ctx, clinicID, practitionerID)
		}
	})
}

// ---------------------------------------------------------------------------
// Horarios (RF5)
// ---------------------------------------------------------------------------

// Schedule devuelve el horario semanal de un profesional de la clínica del
// actor.
func (s *Scheduling) Schedule(ctx context.Context, actor auth.Actor, practitionerID string) ([]ScheduleBlock, error) {
	practitionerID, ok := ids.Normalize(practitionerID)
	if !ok {
		return nil, apperr.Invalid("invalid practitioner id")
	}

	if _, err := s.practitioner(ctx, s.db, actor.ClinicID, practitionerID, false); err != nil {
		return nil, err
	}

	return s.store.Schedule(ctx, s.db, actor.ClinicID, practitionerID)
}

// ReplaceSchedule define el horario semanal completo de un profesional (RF5).
// Un día puede tener varios bloques, por ejemplo mañana y tarde.
//
// Cambiar el horario no cancela las citas ya reservadas: las que quedan fuera
// del horario nuevo se devuelven como conflictos para que recepción las
// reprograme, igual que con un bloqueo (RF6).
func (s *Scheduling) ReplaceSchedule(
	ctx context.Context,
	actor auth.Actor,
	practitionerID string,
	blocks []ScheduleBlock,
) ([]ScheduleBlock, []Appointment, error) {
	practitionerID, ok := ids.Normalize(practitionerID)
	if !ok {
		return nil, nil, apperr.Invalid("invalid practitioner id")
	}

	blocks, err := normalizeSchedule(blocks)
	if err != nil {
		return nil, nil, err
	}

	var (
		schedule  []ScheduleBlock
		conflicts []Appointment
	)

	err = s.tx.Run(ctx, txOptions(actor), func(ctx context.Context, tx *database.Tx) error {
		practitioner, err := s.practitioner(ctx, tx, actor.ClinicID, practitionerID, true)
		if err != nil {
			return err
		}

		if err := ownAgenda(actor, practitioner); err != nil {
			return err
		}

		loc, err := s.location(ctx, tx, actor.ClinicID)
		if err != nil {
			return err
		}

		if err := s.store.ReplaceSchedule(ctx, tx, actor.ClinicID, practitionerID, blocks); err != nil {
			return err
		}

		schedule, err = s.store.Schedule(ctx, tx, actor.ClinicID, practitionerID)
		if err != nil {
			return err
		}

		now := s.now()

		booked, err := s.store.BookedInRange(ctx, tx, actor.ClinicID, practitionerID, now, now.Add(conflictHorizon))
		if err != nil {
			return err
		}

		conflicts = []Appointment{}
		for _, appointment := range booked {
			if !withinSchedule(schedule, appointment.StartsAt, appointment.EndsAt, loc) {
				conflicts = append(conflicts, appointment)
			}
		}

		s.invalidate(ctx, tx, actor.ClinicID, practitionerID)

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return schedule, conflicts, nil
}

// normalizeSchedule valida los bloques de un horario y los ordena. Que dos
// bloques del mismo día no se solapen lo comprueba aquí, para dar un mensaje
// claro, y también la base, con una restricción de exclusión.
func normalizeSchedule(blocks []ScheduleBlock) ([]ScheduleBlock, error) {
	result := make([]ScheduleBlock, 0, len(blocks))

	for _, block := range blocks {
		if block.Weekday < 1 || block.Weekday > 7 {
			return nil, apperr.Invalid("weekday must be between 1 (Monday) and 7 (Sunday)")
		}

		start, okStart := parseClock(strings.TrimSpace(block.StartTime))
		end, okEnd := parseClock(strings.TrimSpace(block.EndTime))
		if !okStart || !okEnd {
			return nil, apperr.Invalid("start_time and end_time must use the HH:MM format")
		}

		if start >= end {
			return nil, apperr.Invalid("start_time must be before end_time")
		}

		if start == 24*60 {
			return nil, apperr.Invalid("start_time must be before 24:00")
		}

		result = append(result, ScheduleBlock{
			Weekday:   block.Weekday,
			StartTime: strings.TrimSpace(block.StartTime),
			EndTime:   strings.TrimSpace(block.EndTime),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Weekday != result[j].Weekday {
			return result[i].Weekday < result[j].Weekday
		}

		return result[i].StartTime < result[j].StartTime
	})

	for i := 1; i < len(result); i++ {
		previous, current := result[i-1], result[i]

		// Las horas van en HH:MM, así que comparar los textos es comparar
		// las horas.
		if previous.Weekday == current.Weekday && current.StartTime < previous.EndTime {
			return nil, apperr.Invalid("schedule blocks of the same day must not overlap")
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Bloqueos (RF6)
// ---------------------------------------------------------------------------

// NewAgendaBlock es la entrada para registrar un bloqueo.
type NewAgendaBlock struct {
	StartsAt string
	EndsAt   string
	Reason   string
}

// CreateBlock registra un bloqueo de agenda (RF6). El bloqueo retira la
// disponibilidad hacia adelante, pero no cancela las citas ya reservadas que
// caen dentro de él: esas se devuelven como conflictos.
func (s *Scheduling) CreateBlock(
	ctx context.Context,
	actor auth.Actor,
	practitionerID string,
	in NewAgendaBlock,
) (AgendaBlock, []Appointment, error) {
	practitionerID, ok := ids.Normalize(practitionerID)
	if !ok {
		return AgendaBlock{}, nil, apperr.Invalid("invalid practitioner id")
	}

	startsAt, err := parseInstant(in.StartsAt, "starts_at")
	if err != nil {
		return AgendaBlock{}, nil, err
	}

	endsAt, err := parseInstant(in.EndsAt, "ends_at")
	if err != nil {
		return AgendaBlock{}, nil, err
	}

	if !startsAt.Before(endsAt) {
		return AgendaBlock{}, nil, apperr.Invalid("starts_at must be before ends_at")
	}

	reason := strings.TrimSpace(in.Reason)
	if !validText(reason, maxBlockReasonLength) {
		return AgendaBlock{}, nil, apperr.Invalid("reason is required (up to 200 characters)")
	}

	var (
		block     AgendaBlock
		conflicts []Appointment
	)

	err = s.tx.Run(ctx, txOptions(actor), func(ctx context.Context, tx *database.Tx) error {
		practitioner, err := s.practitioner(ctx, tx, actor.ClinicID, practitionerID, true)
		if err != nil {
			return err
		}

		if err := ownAgenda(actor, practitioner); err != nil {
			return err
		}

		block, err = s.store.InsertBlock(ctx, tx, AgendaBlock{
			ClinicID:       actor.ClinicID,
			PractitionerID: practitionerID,
			StartsAt:       startsAt,
			EndsAt:         endsAt,
			Reason:         reason,
			CreatedBy:      actor.Subject,
		})
		if err != nil {
			return err
		}

		conflicts, err = s.store.BookedInRange(ctx, tx, actor.ClinicID, practitionerID, startsAt, endsAt)
		if err != nil {
			return err
		}

		s.invalidate(ctx, tx, actor.ClinicID, practitionerID)

		return nil
	})
	if err != nil {
		return AgendaBlock{}, nil, err
	}

	return block, conflicts, nil
}

// Blocks devuelve los bloqueos de un profesional que tocan el rango de fechas
// [from, to], ambas en formato AAAA-MM-DD y en la zona horaria de la clínica.
// Sin fechas devuelve los bloqueos desde hoy.
func (s *Scheduling) Blocks(ctx context.Context, actor auth.Actor, practitionerID, from, to string) ([]AgendaBlock, error) {
	practitionerID, ok := ids.Normalize(practitionerID)
	if !ok {
		return nil, apperr.Invalid("invalid practitioner id")
	}

	if _, err := s.practitioner(ctx, s.db, actor.ClinicID, practitionerID, false); err != nil {
		return nil, err
	}

	loc, err := s.location(ctx, s.db, actor.ClinicID)
	if err != nil {
		return nil, err
	}

	start := startOfDay(s.now(), loc)
	if from != "" {
		if start, err = parseDate(from, "from", loc); err != nil {
			return nil, err
		}
	}

	end := start.Add(conflictHorizon)
	if to != "" {
		day, err := parseDate(to, "to", loc)
		if err != nil {
			return nil, err
		}

		end = day.AddDate(0, 0, 1)
	}

	if !start.Before(end) {
		return nil, apperr.Invalid("from must not be after to")
	}

	return s.store.BlocksInRange(ctx, s.db, actor.ClinicID, practitionerID, start, end)
}

// BlockConflicts devuelve las citas vigentes que caen dentro de un bloqueo
// (RF6): las que recepción debe reprogramar.
func (s *Scheduling) BlockConflicts(ctx context.Context, actor auth.Actor, practitionerID, blockID string) ([]Appointment, error) {
	practitionerID, okPractitioner := ids.Normalize(practitionerID)
	blockID, okBlock := ids.Normalize(blockID)
	if !okPractitioner || !okBlock {
		return nil, apperr.Invalid("invalid id")
	}

	block, err := s.store.Block(ctx, s.db, actor.ClinicID, practitionerID, blockID)
	if err != nil {
		return nil, notFound(err, "block not found")
	}

	return s.store.BookedInRange(ctx, s.db, actor.ClinicID, practitionerID, block.StartsAt, block.EndsAt)
}

// DeleteBlock elimina un bloqueo y devuelve su disponibilidad.
func (s *Scheduling) DeleteBlock(ctx context.Context, actor auth.Actor, practitionerID, blockID string) error {
	practitionerID, okPractitioner := ids.Normalize(practitionerID)
	blockID, okBlock := ids.Normalize(blockID)
	if !okPractitioner || !okBlock {
		return apperr.Invalid("invalid id")
	}

	return s.tx.Run(ctx, txOptions(actor), func(ctx context.Context, tx *database.Tx) error {
		practitioner, err := s.practitioner(ctx, tx, actor.ClinicID, practitionerID, true)
		if err != nil {
			return err
		}

		if err := ownAgenda(actor, practitioner); err != nil {
			return err
		}

		if err := s.store.DeleteBlock(ctx, tx, actor.ClinicID, practitionerID, blockID); err != nil {
			return err
		}

		s.invalidate(ctx, tx, actor.ClinicID, practitionerID)

		return nil
	})
}

// ---------------------------------------------------------------------------
// Disponibilidad (RF7, RF8)
// ---------------------------------------------------------------------------

// Availability devuelve los espacios libres de un profesional en una fecha
// para un servicio (RF7). El segundo valor indica si la respuesta salió de la
// caché.
//
// Es la consulta que más se ejecuta, así que un acierto de caché no toca la
// base: ni siquiera para comprobar que el profesional existe. No hace falta,
// porque la clave lleva la clínica del actor y una entrada solo se guarda
// después de haber validado al profesional y al servicio en esa clínica. Todo
// cambio que afecte a esos datos sube la versión de la agenda.
func (s *Scheduling) Availability(
	ctx context.Context,
	actor auth.Actor,
	practitionerID, serviceID, date string,
) ([]Slot, bool, error) {
	practitionerID, okPractitioner := ids.Normalize(practitionerID)
	serviceID, okService := ids.Normalize(serviceID)
	if !okPractitioner || !okService {
		return nil, false, apperr.Invalid("practitioner id and service_id must be valid ids")
	}

	date = strings.TrimSpace(date)
	if _, err := time.Parse(dateLayout, date); err != nil {
		return nil, false, apperr.Invalid("date must use the YYYY-MM-DD format")
	}

	entry := s.lookup(ctx, actor.ClinicID, practitionerID, serviceID, date)
	if entry.hit {
		return s.upcoming(entry.slots), true, nil
	}

	loc, err := s.location(ctx, s.db, actor.ClinicID)
	if err != nil {
		return nil, false, err
	}

	day, err := parseDate(date, "date", loc)
	if err != nil {
		return nil, false, err
	}

	practitioner, err := s.practitioner(ctx, s.db, actor.ClinicID, practitionerID, false)
	if err != nil {
		return nil, false, err
	}

	service, err := s.store.Service(ctx, s.db, actor.ClinicID, serviceID)
	if err != nil {
		return nil, false, notFound(err, "service not found")
	}

	offers, err := s.store.PractitionerOffers(ctx, s.db, actor.ClinicID, practitionerID, serviceID)
	if err != nil {
		return nil, false, err
	}

	// Un profesional inactivo, un servicio retirado o un servicio que el
	// profesional no atiende no tienen espacios: reservar ahí se rechazaría.
	if !practitioner.Active || !service.Active || !offers {
		return []Slot{}, false, nil
	}

	slots, err := s.computeSlots(ctx, actor.ClinicID, practitionerID, service, day, loc)
	if err != nil {
		return nil, false, err
	}

	s.remember(ctx, entry, slots)

	return s.upcoming(slots), false, nil
}

// AvailabilityByService devuelve, para un servicio y un rango de fechas, los
// profesionales que tienen espacio y cuáles son esos espacios (RF8). Cada
// agenda de cada día sale de la misma caché que RF7.
func (s *Scheduling) AvailabilityByService(
	ctx context.Context,
	actor auth.Actor,
	serviceID, from, to string,
) ([]PractitionerSlots, error) {
	serviceID, ok := ids.Normalize(serviceID)
	if !ok {
		return nil, apperr.Invalid("service_id must be a valid id")
	}

	loc, err := s.location(ctx, s.db, actor.ClinicID)
	if err != nil {
		return nil, err
	}

	start, err := parseDate(from, "from", loc)
	if err != nil {
		return nil, err
	}

	end, err := parseDate(to, "to", loc)
	if err != nil {
		return nil, err
	}

	if end.Before(start) {
		return nil, apperr.Invalid("from must not be after to")
	}

	if end.After(start.AddDate(0, 0, maxAvailabilityDays-1)) {
		return nil, apperr.Invalid("the date range must not exceed 31 days")
	}

	service, err := s.store.Service(ctx, s.db, actor.ClinicID, serviceID)
	if err != nil {
		return nil, notFound(err, "service not found")
	}

	result := []PractitionerSlots{}

	if !service.Active {
		return result, nil
	}

	practitioners, err := s.store.PractitionersForService(ctx, s.db, actor.ClinicID, serviceID)
	if err != nil {
		return nil, err
	}

	for _, practitioner := range practitioners {
		for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
			entry := s.lookup(ctx, actor.ClinicID, practitioner.ID, service.ID, day.Format(dateLayout))

			slots := entry.slots
			if !entry.hit {
				if slots, err = s.computeSlots(ctx, actor.ClinicID, practitioner.ID, service, day, loc); err != nil {
					return nil, err
				}

				s.remember(ctx, entry, slots)
			}

			if slots = s.upcoming(slots); len(slots) == 0 {
				continue
			}

			result = append(result, PractitionerSlots{
				PractitionerID: practitioner.ID,
				DisplayName:    practitioner.DisplayName,
				Date:           day.Format(dateLayout),
				Slots:          slots,
			})
		}
	}

	return result, nil
}

// cacheEntry es el resultado de buscar una agenda en la caché: los espacios
// si hubo acierto y, si no, la clave bajo la que se debe guardar el cálculo.
type cacheEntry struct {
	key    string
	slots  []Slot
	hit    bool
	usable bool
}

// lookup busca en la caché los espacios de una agenda en un día.
//
// La versión de la agenda se lee aquí, antes de que nadie consulte la base, y
// queda dentro de la clave. Si un cambio hace commit mientras se calcula, la
// versión sube y lo calculado se guarda bajo una clave que ya nadie pide: una
// lectura vieja nunca puede pisar una invalidación (I8).
func (s *Scheduling) lookup(ctx context.Context, clinicID, practitionerID, serviceID, date string) cacheEntry {
	version, usable := s.cache.Version(ctx, clinicID, practitionerID)

	entry := cacheEntry{
		key:    availabilityKey(clinicID, practitionerID, date, serviceID, version),
		usable: usable,
	}

	if usable {
		entry.slots, entry.hit = s.cache.Get(ctx, entry.key)
	}

	return entry
}

// remember guarda lo calculado bajo la clave que devolvió lookup.
func (s *Scheduling) remember(ctx context.Context, entry cacheEntry, slots []Slot) {
	if entry.usable {
		s.cache.Set(ctx, entry.key, slots)
	}
}

// InvalidateAgendas sube la versión de las agendas de esos profesionales. Lo
// usa el catálogo cuando cambia algo de lo que depende la disponibilidad pero
// que no pasa por este servicio: el estado de un profesional o los servicios
// que atiende.
func (s *Scheduling) InvalidateAgendas(ctx context.Context, clinicID string, practitionerIDs ...string) {
	for _, practitionerID := range practitionerIDs {
		s.cache.Invalidate(ctx, clinicID, practitionerID)
	}
}

// InvalidateService sube la versión de las agendas de todos los profesionales
// que atienden un servicio. Se usa cuando el servicio se retira del catálogo.
func (s *Scheduling) InvalidateService(ctx context.Context, clinicID, serviceID string) {
	practitioners, err := s.store.PractitionersForService(ctx, s.db, clinicID, serviceID)
	if err != nil {
		return
	}

	for _, practitioner := range practitioners {
		s.cache.Invalidate(ctx, clinicID, practitioner.ID)
	}
}

// computeSlots calcula los espacios de un día contra la base.
func (s *Scheduling) computeSlots(
	ctx context.Context,
	clinicID, practitionerID string,
	service serviceRef,
	day time.Time,
	loc *time.Location,
) ([]Slot, error) {
	schedule, err := s.store.Schedule(ctx, s.db, clinicID, practitionerID)
	if err != nil {
		return nil, err
	}

	working := workingIntervals(schedule, day, loc)
	if len(working) == 0 {
		return []Slot{}, nil
	}

	from, to := working[0].start, working[len(working)-1].end

	blocks, err := s.store.BlocksInRange(ctx, s.db, clinicID, practitionerID, from, to)
	if err != nil {
		return nil, err
	}

	booked, err := s.store.BookedInRange(ctx, s.db, clinicID, practitionerID, from, to)
	if err != nil {
		return nil, err
	}

	return availableSlots(schedule, blocks, booked, day, loc, service.Duration), nil
}

// upcoming quita los espacios que ya empezaron. Se aplica después de la caché
// y no antes, porque depende de la hora y no del estado de la base: una
// entrada guardada hace un rato no debe ofrecer un espacio que ya pasó.
func (s *Scheduling) upcoming(slots []Slot) []Slot {
	now := s.now()
	result := make([]Slot, 0, len(slots))

	for _, slot := range slots {
		if !slot.StartsAt.Before(now) {
			result = append(result, slot)
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Ciclo de vida de la cita (RF9 a RF13)
// ---------------------------------------------------------------------------

// NewBooking es la entrada para reservar una cita.
type NewBooking struct {
	PatientID      string
	PractitionerID string
	ServiceID      string
	StartsAt       string
}

// Book reserva una cita (RF9). Es la operación crítica del sistema.
//
// Tiene tres desenlaces: la cita creada, un conflicto porque el espacio no
// está disponible (ya tomado, bloqueado o fuera del horario) y una entrada
// inválida. Si dos reservas compiten por el mismo espacio, la fila del
// profesional las pone en fila y la restricción de exclusión rechaza a la
// segunda: gana exactamente una.
func (s *Scheduling) Book(ctx context.Context, actor auth.Actor, in NewBooking) (Appointment, error) {
	patientID, okPatient := ids.Normalize(in.PatientID)
	practitionerID, okPractitioner := ids.Normalize(in.PractitionerID)
	serviceID, okService := ids.Normalize(in.ServiceID)
	if !okPatient || !okPractitioner || !okService {
		return Appointment{}, apperr.Invalid("patient_id, practitioner_id and service_id must be valid ids")
	}

	startsAt, err := parseInstant(in.StartsAt, "starts_at")
	if err != nil {
		return Appointment{}, err
	}

	if startsAt.Before(s.now()) {
		return Appointment{}, apperr.Invalid("starts_at must not be in the past")
	}

	var appointment Appointment

	err = s.tx.Run(ctx, txOptions(actor), func(ctx context.Context, tx *database.Tx) error {
		practitioner, err := s.practitioner(ctx, tx, actor.ClinicID, practitionerID, true)
		if err != nil {
			return err
		}

		if err := s.checkSlot(ctx, tx, actor.ClinicID, patientID, practitioner, serviceID, startsAt); err != nil {
			return err
		}

		appointment, err = s.store.InsertAppointment(ctx, tx, actor.ClinicID, newAppointment{
			PatientID:      patientID,
			PractitionerID: practitionerID,
			ServiceID:      serviceID,
			StartsAt:       startsAt,
			CreatedBy:      actor.Subject,
		})
		if err != nil {
			return err
		}

		s.invalidate(ctx, tx, actor.ClinicID, practitionerID)

		return nil
	})
	if err != nil {
		return Appointment{}, err
	}

	return appointment, nil
}

// checkSlot valida todo lo que una reserva necesita y que la base no puede
// garantizar con una restricción: que el profesional esté activo y atienda el
// servicio, que el paciente exista, y que la cita caiga dentro del horario y
// fuera de los bloqueos (I3).
//
// Debe correr con la fila del profesional ya bloqueada. Como cambiar el
// horario y crear un bloqueo toman el mismo candado, lo que aquí se lee sigue
// siendo cierto cuando se inserta la cita.
func (s *Scheduling) checkSlot(
	ctx context.Context,
	tx *database.Tx,
	clinicID, patientID string,
	practitioner practitionerRef,
	serviceID string,
	startsAt time.Time,
) error {
	practitionerID := practitioner.ID

	if !practitioner.Active {
		return apperr.Conflict("the practitioner is inactive")
	}

	service, err := s.store.Service(ctx, tx, clinicID, serviceID)
	if err != nil {
		return notFound(err, "service not found")
	}

	if !service.Active {
		return apperr.Conflict("the service is inactive")
	}

	offers, err := s.store.PractitionerOffers(ctx, tx, clinicID, practitionerID, serviceID)
	if err != nil {
		return err
	}

	if !offers {
		return apperr.Conflict("the practitioner does not offer that service")
	}

	active, err := s.store.PatientActive(ctx, tx, clinicID, patientID)
	if err != nil {
		return notFound(err, "patient not found")
	}

	if !active {
		return apperr.Conflict("the patient is inactive")
	}

	loc, err := s.location(ctx, tx, clinicID)
	if err != nil {
		return err
	}

	endsAt := startsAt.Add(service.Duration)

	schedule, err := s.store.Schedule(ctx, tx, clinicID, practitionerID)
	if err != nil {
		return err
	}

	if !withinSchedule(schedule, startsAt, endsAt, loc) {
		return apperr.Conflict("the slot is outside the practitioner's schedule")
	}

	blocks, err := s.store.BlocksInRange(ctx, tx, clinicID, practitionerID, startsAt, endsAt)
	if err != nil {
		return err
	}

	if len(blocks) > 0 {
		return apperr.Conflict("the practitioner's agenda is blocked at that time")
	}

	return nil
}

// GetAppointment devuelve una cita de la clínica del actor.
func (s *Scheduling) GetAppointment(ctx context.Context, actor auth.Actor, appointmentID string) (Appointment, error) {
	appointmentID, ok := ids.Normalize(appointmentID)
	if !ok {
		return Appointment{}, apperr.Invalid("invalid appointment id")
	}

	appointment, err := s.store.Appointment(ctx, s.db, actor.ClinicID, appointmentID, false)

	return appointment, notFound(err, "appointment not found")
}

// Rescheduling es la entrada para reprogramar una cita.
type Rescheduling struct {
	StartsAt string
	// PractitionerID es opcional: vacío conserva al mismo profesional.
	PractitionerID string
}

// Reschedule mueve una cita vigente a otro espacio (RF10). La cita anterior
// queda en rescheduled y se crea una nueva que apunta a ella, todo en una
// transacción: en ningún momento existen las dos vigentes ni los dos espacios
// ocupados, y si el espacio nuevo no se puede tomar la cita anterior queda
// como estaba.
//
// Puede cambiar de profesional. En ese caso se bloquean las filas de los dos
// profesionales, siempre en orden de id: dos reprogramaciones cruzadas (de A
// a B y de B a A) piden los candados en el mismo orden y no pueden formar un
// deadlock. Si PostgreSQL detectara uno de todas formas, TxManager repite la
// transacción.
func (s *Scheduling) Reschedule(ctx context.Context, actor auth.Actor, appointmentID string, in Rescheduling) (Appointment, error) {
	appointmentID, ok := ids.Normalize(appointmentID)
	if !ok {
		return Appointment{}, apperr.Invalid("invalid appointment id")
	}

	startsAt, err := parseInstant(in.StartsAt, "starts_at")
	if err != nil {
		return Appointment{}, err
	}

	if startsAt.Before(s.now()) {
		return Appointment{}, apperr.Invalid("starts_at must not be in the past")
	}

	targetID := ""
	if strings.TrimSpace(in.PractitionerID) != "" {
		if targetID, ok = ids.Normalize(in.PractitionerID); !ok {
			return Appointment{}, apperr.Invalid("invalid practitioner_id")
		}
	}

	var created Appointment

	err = s.tx.Run(ctx, txOptions(actor), func(ctx context.Context, tx *database.Tx) error {
		// Primera lectura sin candado, solo para saber de qué profesional es
		// la cita. Ese dato no cambia nunca (lo impide un trigger).
		current, err := s.store.Appointment(ctx, tx, actor.ClinicID, appointmentID, false)
		if err != nil {
			return notFound(err, "appointment not found")
		}

		target := targetID
		if target == "" {
			target = current.PractitionerID
		}

		lockOrder := []string{current.PractitionerID}
		if target != current.PractitionerID {
			lockOrder = append(lockOrder, target)
			sort.Strings(lockOrder)
		}

		locked := make(map[string]practitionerRef, len(lockOrder))

		for _, practitionerID := range lockOrder {
			ref, err := s.practitioner(ctx, tx, actor.ClinicID, practitionerID, true)
			if err != nil {
				return err
			}

			locked[practitionerID] = ref
		}

		// Con los profesionales bloqueados se vuelve a leer la cita, ahora
		// con candado: otra petición pudo cerrarla mientras tanto.
		current, err = s.store.Appointment(ctx, tx, actor.ClinicID, appointmentID, true)
		if err != nil {
			return notFound(err, "appointment not found")
		}

		if !canTransition(current.Status, StatusRescheduled) {
			return apperr.Conflict("the appointment is " + current.Status + " and cannot be rescheduled")
		}

		if target == current.PractitionerID && startsAt.Equal(current.StartsAt) {
			return apperr.Invalid("the appointment is already at that time")
		}

		// El orden importa: primero se libera el espacio anterior y después
		// se ocupa el nuevo, porque las restricciones de exclusión se
		// comprueban en cada sentencia. Así la cita puede moverse a un
		// espacio que se solapa con el que tenía.
		if _, closed, err := s.store.CloseAppointment(
			ctx, tx, actor.ClinicID, appointmentID, StatusRescheduled, actor.Subject, nil, false,
		); err != nil {
			return err
		} else if !closed {
			return apperr.Conflict("the appointment can no longer be rescheduled")
		}

		if err := s.checkSlot(ctx, tx, actor.ClinicID, current.PatientID, locked[target], current.ServiceID, startsAt); err != nil {
			return err
		}

		created, err = s.store.InsertAppointment(ctx, tx, actor.ClinicID, newAppointment{
			PatientID:         current.PatientID,
			PractitionerID:    target,
			ServiceID:         current.ServiceID,
			StartsAt:          startsAt,
			CreatedBy:         actor.Subject,
			RescheduledFromID: &current.ID,
		})
		if err != nil {
			return err
		}

		s.invalidate(ctx, tx, actor.ClinicID, lockOrder...)

		return nil
	})
	if err != nil {
		return Appointment{}, err
	}

	return created, nil
}

// Cancel cancela una cita vigente y deja registrado el motivo y quién la
// canceló (RF11). Su espacio queda libre en la misma transacción (I7).
func (s *Scheduling) Cancel(ctx context.Context, actor auth.Actor, appointmentID, reason string) (Appointment, error) {
	appointmentID, ok := ids.Normalize(appointmentID)
	if !ok {
		return Appointment{}, apperr.Invalid("invalid appointment id")
	}

	reason = strings.TrimSpace(reason)
	if !validText(reason, maxCancellationReasonLength) {
		return Appointment{}, apperr.Invalid("reason is required (up to 300 characters)")
	}

	return s.finish(ctx, actor, appointmentID, StatusCancelled, &reason, false)
}

// RecordOutcome registra el desenlace de una cita: atendida o ausencia
// (RF12). Solo se admite cuando ya pasó su hora de inicio.
func (s *Scheduling) RecordOutcome(ctx context.Context, actor auth.Actor, appointmentID, outcome string) (Appointment, error) {
	appointmentID, ok := ids.Normalize(appointmentID)
	if !ok {
		return Appointment{}, apperr.Invalid("invalid appointment id")
	}

	outcome = strings.TrimSpace(outcome)
	if outcome != StatusAttended && outcome != StatusNoShow {
		return Appointment{}, apperr.Invalid("outcome must be attended or no_show")
	}

	return s.finish(ctx, actor, appointmentID, outcome, nil, true)
}

// finish saca una cita de booked. Si no se pudo, lee la cita para explicar por
// qué: no existe, ya está en un estado final (RF13) o todavía no empieza
// (RF12).
func (s *Scheduling) finish(
	ctx context.Context,
	actor auth.Actor,
	appointmentID, status string,
	reason *string,
	onlyIfStarted bool,
) (Appointment, error) {
	var appointment Appointment

	err := s.tx.Run(ctx, txOptions(actor), func(ctx context.Context, tx *database.Tx) error {
		closed, ok, err := s.store.CloseAppointment(
			ctx, tx, actor.ClinicID, appointmentID, status, actor.Subject, reason, onlyIfStarted,
		)
		if err != nil {
			return err
		}

		if ok {
			appointment = closed
			s.invalidate(ctx, tx, actor.ClinicID, closed.PractitionerID)

			return nil
		}

		current, err := s.store.Appointment(ctx, tx, actor.ClinicID, appointmentID, false)
		if err != nil {
			return notFound(err, "appointment not found")
		}

		if !canTransition(current.Status, status) {
			return apperr.Conflict("the appointment is " + current.Status + " and cannot change")
		}

		if onlyIfStarted {
			return apperr.Conflict("the appointment has not started yet")
		}

		return apperr.Conflict("the appointment could not be closed; try again")
	})
	if err != nil {
		return Appointment{}, err
	}

	return appointment, nil
}

// ClinicalNoteContext devuelve lo que Expediente necesita para decidir si
// acepta una nota clínica (RF15). Expediente lo pide con el token del
// profesional, así que aquí aplica el mismo filtro por clínica que a
// cualquier otra petición.
func (s *Scheduling) ClinicalNoteContext(ctx context.Context, actor auth.Actor, appointmentID string) (noteContext, error) {
	appointmentID, ok := ids.Normalize(appointmentID)
	if !ok {
		return noteContext{}, apperr.Invalid("invalid appointment id")
	}

	result, err := s.store.NoteContext(ctx, s.db, actor.ClinicID, appointmentID)

	return result, notFound(err, "appointment not found")
}

// ---------------------------------------------------------------------------
// Apoyo
// ---------------------------------------------------------------------------

// practitioner devuelve un profesional de la clínica, con candado si lock es
// true.
func (s *Scheduling) practitioner(
	ctx context.Context,
	db database.DBTX,
	clinicID, practitionerID string,
	lock bool,
) (practitionerRef, error) {
	ref, err := s.store.Practitioner(ctx, db, clinicID, practitionerID, lock)

	return ref, notFound(err, "practitioner not found")
}

// location devuelve la zona horaria de la clínica.
func (s *Scheduling) location(ctx context.Context, db database.DBTX, clinicID string) (*time.Location, error) {
	timezone, err := s.store.ClinicTimezone(ctx, db, clinicID)
	if err != nil {
		return nil, notFound(err, "clinic not found")
	}

	return time.LoadLocation(timezone)
}

// ownAgenda aplica la regla "solo los propios" de la matriz de permisos para
// RF5 y RF6: un profesional solo administra su propio horario y sus propios
// bloqueos. La administración de la clínica administra los de todos.
func ownAgenda(actor auth.Actor, practitioner practitionerRef) error {
	if actor.Role == auth.RolePractitioner && practitioner.UserSubject != actor.Subject {
		return apperr.Forbidden("practitioners can only manage their own agenda")
	}

	return nil
}

// notFound sustituye el "no encontrado" genérico por un mensaje que dice qué
// faltó. Los demás errores pasan sin cambios.
func notFound(err error, message string) error {
	if err != nil && errors.Is(err, apperr.ErrNotFound) {
		return apperr.NotFound(message)
	}

	return err
}

// parseInstant lee un instante en formato RFC 3339, con zona horaria.
func parseInstant(value, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, apperr.Invalid(field + " must be an RFC 3339 timestamp, for example 2026-03-04T09:00:00-06:00")
	}

	return parsed, nil
}

// parseDate lee una fecha AAAA-MM-DD y devuelve su medianoche en loc.
func parseDate(value, field string, loc *time.Location) (time.Time, error) {
	parsed, err := time.ParseInLocation(dateLayout, strings.TrimSpace(value), loc)
	if err != nil {
		return time.Time{}, apperr.Invalid(field + " must use the YYYY-MM-DD format")
	}

	return parsed, nil
}

// startOfDay devuelve la medianoche de t en loc.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	year, month, day := t.In(loc).Date()

	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}
