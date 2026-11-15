package agenda

import (
	"errors"
	"testing"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
)

// costaRica es la zona de las clínicas de ejemplo: UTC-6, sin horario de
// verano.
var costaRica = mustLocation("America/Costa_Rica")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}

	return loc
}

// at devuelve un instante del lunes 4 de marzo de 2030 en Costa Rica.
func at(hour, minute int) time.Time {
	return time.Date(2030, time.March, 4, hour, minute, 0, 0, costaRica)
}

// splitDay es una jornada partida de lunes: mañana y tarde.
var splitDay = []ScheduleBlock{
	{Weekday: 1, StartTime: "08:00", EndTime: "12:00"},
	{Weekday: 1, StartTime: "13:00", EndTime: "17:00"},
}

func starts(slots []Slot) []string {
	result := make([]string, 0, len(slots))
	for _, slot := range slots {
		result = append(result, slot.StartsAt.Format("15:04"))
	}

	return result
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// Cubre RF13 (estados de la cita)
//
// Desde booked se llega a los cuatro estados finales, y de un estado final
// no se sale.
func TestCanTransition(t *testing.T) {
	finals := []string{StatusAttended, StatusNoShow, StatusCancelled, StatusRescheduled}

	for _, to := range finals {
		if !canTransition(StatusBooked, to) {
			t.Errorf("booked -> %s must be allowed", to)
		}
	}

	if canTransition(StatusBooked, StatusBooked) {
		t.Error("booked -> booked is not a transition")
	}

	for _, from := range finals {
		for _, to := range append(finals, StatusBooked) {
			if canTransition(from, to) {
				t.Errorf("%s -> %s must be rejected: %s is final", from, to, from)
			}
		}
	}
}

func TestParseClock(t *testing.T) {
	valid := map[string]int{"00:00": 0, "08:30": 510, "23:59": 1439, "24:00": 1440}
	for value, want := range valid {
		if got, ok := parseClock(value); !ok || got != want {
			t.Errorf("parseClock(%q) = %d, %v; want %d", value, got, ok, want)
		}
	}

	for _, value := range []string{"", "8:00", "08:60", "24:01", "25:00", "08-00", "ab:cd", "08:00:00"} {
		if _, ok := parseClock(value); ok {
			t.Errorf("parseClock(%q) must fail", value)
		}
	}
}

// Cubre RF7 (disponibilidad a partir del horario y de la duración)
func TestAvailableSlotsFollowScheduleAndDuration(t *testing.T) {
	slots := availableSlots(splitDay, nil, nil, at(0, 0), costaRica, 60*time.Minute)

	want := []string{"08:00", "09:00", "10:00", "11:00", "13:00", "14:00", "15:00", "16:00"}
	if got := starts(slots); !equal(got, want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}

	// Un servicio de 45 minutos no cabe una sexta vez en cuatro horas, y
	// ningún espacio cruza la pausa del mediodía.
	slots = availableSlots(splitDay, nil, nil, at(0, 0), costaRica, 45*time.Minute)

	want = []string{"08:00", "08:45", "09:30", "10:15", "11:00", "13:00", "13:45", "14:30", "15:15", "16:00"}
	if got := starts(slots); !equal(got, want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}
}

// Cubre RF7 (los bloqueos y las citas vigentes quitan disponibilidad)
func TestAvailableSlotsSkipBlocksAndAppointments(t *testing.T) {
	blocks := []AgendaBlock{{StartsAt: at(9, 0), EndsAt: at(10, 0)}}
	booked := []Appointment{{StartsAt: at(10, 0).UTC(), EndsAt: at(10, 20).UTC()}}

	slots := availableSlots(splitDay, blocks, booked, at(0, 0), costaRica, 30*time.Minute)

	// Después de la cita de 20 minutos los espacios siguen desde las 10:20,
	// sin dejar tiempo muerto.
	want := []string{"08:00", "08:30", "10:20", "10:50", "11:20", "13:00", "13:30", "14:00", "14:30", "15:00", "15:30", "16:00", "16:30"}
	if got := starts(slots); !equal(got, want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}

	for _, slot := range slots {
		if slot.StartsAt.Location() != costaRica {
			t.Fatalf("slot %v is not in the clinic's time zone", slot.StartsAt)
		}
	}
}

func TestAvailableSlotsOnADayWithoutSchedule(t *testing.T) {
	tuesday := at(0, 0).AddDate(0, 0, 1)

	if slots := availableSlots(splitDay, nil, nil, tuesday, costaRica, 30*time.Minute); len(slots) != 0 {
		t.Fatalf("got %d slots on a day without schedule", len(slots))
	}
}

// El mismo horario produce instantes distintos según la zona de la clínica.
func TestScheduleIsInTheClinicTimeZone(t *testing.T) {
	bogota := mustLocation("America/Bogota") // UTC-5

	day := time.Date(2030, time.March, 4, 0, 0, 0, 0, time.UTC)

	inCostaRica := availableSlots(splitDay, nil, nil, day.In(costaRica).AddDate(0, 0, 1), costaRica, time.Hour)
	inBogota := availableSlots(splitDay, nil, nil, day.In(bogota).AddDate(0, 0, 1), bogota, time.Hour)

	if len(inCostaRica) == 0 || len(inBogota) == 0 {
		t.Fatal("expected slots in both clinics")
	}

	if difference := inCostaRica[0].StartsAt.Sub(inBogota[0].StartsAt); difference != time.Hour {
		t.Fatalf("08:00 in Costa Rica is %s after 08:00 in Bogotá, want 1h", difference)
	}
}

// Cubre I3 (la cita cae dentro del horario de atención)
func TestWithinSchedule(t *testing.T) {
	cases := []struct {
		name       string
		start, end time.Time
		want       bool
	}{
		{"dentro de la mañana", at(9, 0), at(9, 30), true},
		{"termina justo al cerrar", at(11, 30), at(12, 0), true},
		{"empieza al abrir la tarde", at(13, 0), at(13, 45), true},
		{"antes de abrir", at(7, 30), at(8, 0), false},
		{"empieza antes y termina dentro", at(7, 45), at(8, 15), false},
		{"cruza a la pausa", at(11, 45), at(12, 15), false},
		{"en la pausa", at(12, 15), at(12, 45), false},
		{"después de cerrar", at(17, 0), at(17, 30), false},
		{"otro día de la semana", at(9, 0).AddDate(0, 0, 1), at(9, 30).AddDate(0, 0, 1), false},
	}

	for _, tc := range cases {
		if got := withinSchedule(splitDay, tc.start.UTC(), tc.end.UTC(), costaRica); got != tc.want {
			t.Errorf("%s: withinSchedule = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Dos bloques contiguos se comportan como uno solo, también cuando el primero
// termina a la medianoche y el segundo empieza el día siguiente.
func TestContiguousBlocksAreMerged(t *testing.T) {
	schedule := []ScheduleBlock{
		{Weekday: 1, StartTime: "08:00", EndTime: "12:00"},
		{Weekday: 1, StartTime: "12:00", EndTime: "16:00"},
		{Weekday: 1, StartTime: "22:00", EndTime: "24:00"},
		{Weekday: 2, StartTime: "00:00", EndTime: "02:00"},
	}

	if !withinSchedule(schedule, at(11, 45), at(12, 15), costaRica) {
		t.Error("an appointment across two contiguous blocks must be allowed")
	}

	if !withinSchedule(schedule, at(23, 45), at(24, 15), costaRica) {
		t.Error("an appointment across midnight between contiguous blocks must be allowed")
	}

	if withinSchedule(schedule, at(21, 45), at(22, 15), costaRica) {
		t.Error("an appointment that starts before the block must be rejected")
	}
}

func TestSubtractIntervals(t *testing.T) {
	free := []interval{{at(8, 0), at(12, 0)}}
	busy := []interval{
		{at(7, 0), at(8, 30)},   // se sale por delante
		{at(9, 0), at(9, 30)},   // en medio
		{at(9, 15), at(10, 0)},  // se solapa con el anterior
		{at(11, 30), at(13, 0)}, // se sale por detrás
	}

	got := subtractIntervals(free, busy)

	want := []interval{{at(8, 30), at(9, 0)}, {at(10, 0), at(11, 30)}}
	if len(got) != len(want) {
		t.Fatalf("got %d intervals, want %d: %v", len(got), len(want), got)
	}

	for i := range want {
		if !got[i].start.Equal(want[i].start) || !got[i].end.Equal(want[i].end) {
			t.Errorf("interval %d = %v-%v, want %v-%v", i, got[i].start, got[i].end, want[i].start, want[i].end)
		}
	}
}

// Cubre RF5 (horario semanal con varios bloques por día)
func TestNormalizeSchedule(t *testing.T) {
	valid, err := normalizeSchedule([]ScheduleBlock{
		{Weekday: 2, StartTime: "13:00", EndTime: "17:00"},
		{Weekday: 1, StartTime: " 08:00 ", EndTime: "12:00"},
		{Weekday: 2, StartTime: "08:00", EndTime: "13:00"},
		{Weekday: 7, StartTime: "00:00", EndTime: "24:00"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if valid[0].Weekday != 1 || valid[0].StartTime != "08:00" || valid[1].StartTime != "08:00" || valid[2].StartTime != "13:00" {
		t.Errorf("blocks are not trimmed and sorted: %+v", valid)
	}

	invalid := [][]ScheduleBlock{
		{{Weekday: 0, StartTime: "08:00", EndTime: "12:00"}},
		{{Weekday: 8, StartTime: "08:00", EndTime: "12:00"}},
		{{Weekday: 1, StartTime: "12:00", EndTime: "08:00"}},
		{{Weekday: 1, StartTime: "08:00", EndTime: "08:00"}},
		{{Weekday: 1, StartTime: "8:00", EndTime: "12:00"}},
		{{Weekday: 1, StartTime: "08:00", EndTime: "24:30"}},
		{{Weekday: 1, StartTime: "08:00", EndTime: "12:00"}, {Weekday: 1, StartTime: "11:59", EndTime: "13:00"}},
	}

	for _, blocks := range invalid {
		if _, err := normalizeSchedule(blocks); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%+v: error = %v, want invalid input", blocks, err)
		}
	}

	if empty, err := normalizeSchedule(nil); err != nil || len(empty) != 0 {
		t.Errorf("an empty schedule is valid: %v, %v", empty, err)
	}
}
