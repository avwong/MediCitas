package agenda

import (
	"sort"
	"time"
)

// Estados de una cita (RF13). Solo una cita en booked ocupa espacio en la
// agenda; los otros cuatro son finales.
const (
	StatusBooked      = "booked"
	StatusAttended    = "attended"
	StatusNoShow      = "no_show"
	StatusCancelled   = "cancelled"
	StatusRescheduled = "rescheduled"
)

// dateLayout es el formato de las fechas sin hora en la API.
const dateLayout = "2006-01-02"

// ScheduleBlock es un bloque del horario semanal de un profesional (RF5). Las
// horas están en la zona horaria de la clínica y no llevan fecha: el bloque
// vale para todas las semanas.
type ScheduleBlock struct {
	ID string `json:"id,omitempty"`
	// Weekday va de 1 (lunes) a 7 (domingo), como en ISO 8601.
	Weekday int `json:"weekday"`
	// StartTime y EndTime van en formato HH:MM. EndTime admite 24:00 para un
	// bloque que llega hasta la medianoche.
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// AgendaBlock es un bloqueo de agenda (RF6): un intervalo en el que el
// profesional no atiende.
type AgendaBlock struct {
	ID             string    `json:"id"`
	ClinicID       string    `json:"clinic_id"`
	PractitionerID string    `json:"practitioner_id"`
	StartsAt       time.Time `json:"starts_at"`
	EndsAt         time.Time `json:"ends_at"`
	Reason         string    `json:"reason"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// Appointment es una cita (RF9 a RF13).
type Appointment struct {
	ID             string    `json:"id"`
	ClinicID       string    `json:"clinic_id"`
	PatientID      string    `json:"patient_id"`
	PractitionerID string    `json:"practitioner_id"`
	ServiceID      string    `json:"service_id"`
	StartsAt       time.Time `json:"starts_at"`
	// EndsAt lo fija la base a partir de la duración del servicio (I4).
	EndsAt time.Time `json:"ends_at"`
	Status string    `json:"status"`
	// RescheduledFromID es la cita anterior, si esta nació de una
	// reprogramación (RF10).
	RescheduledFromID  *string    `json:"rescheduled_from_id,omitempty"`
	CreatedBy          string     `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
	ClosedBy           *string    `json:"closed_by,omitempty"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	CancellationReason *string    `json:"cancellation_reason,omitempty"`
}

// Slot es un espacio disponible para reservar.
type Slot struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// PractitionerSlots son los espacios de un profesional en una fecha (RF8).
type PractitionerSlots struct {
	PractitionerID string `json:"practitioner_id"`
	DisplayName    string `json:"display_name"`
	Date           string `json:"date"`
	Slots          []Slot `json:"slots"`
}

// canTransition es la máquina de estados de RF13: desde booked se puede pasar
// a cualquiera de los cuatro estados finales, y de un estado final no se sale.
func canTransition(from, to string) bool {
	if from != StatusBooked {
		return false
	}

	switch to {
	case StatusAttended, StatusNoShow, StatusCancelled, StatusRescheduled:
		return true
	default:
		return false
	}
}

// interval es un intervalo de tiempo semiabierto [start, end).
type interval struct {
	start time.Time
	end   time.Time
}

// parseClock convierte HH:MM a minutos desde la medianoche. Acepta 24:00.
func parseClock(value string) (int, bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}

	for _, i := range []int{0, 1, 3, 4} {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}

	hours := int(value[0]-'0')*10 + int(value[1]-'0')
	minutes := int(value[3]-'0')*10 + int(value[4]-'0')

	if minutes > 59 || hours > 24 || (hours == 24 && minutes != 0) {
		return 0, false
	}

	return hours*60 + minutes, true
}

// isoWeekday devuelve el día de la semana de 1 (lunes) a 7 (domingo).
func isoWeekday(t time.Time) int {
	if t.Weekday() == time.Sunday {
		return 7
	}

	return int(t.Weekday())
}

// workingIntervals convierte el horario semanal en intervalos concretos para
// la fecha de day, en la zona horaria de la clínica. Los bloques contiguos se
// unen, de modo que una cita puede cruzar de un bloque al siguiente si no hay
// pausa entre ellos.
func workingIntervals(schedule []ScheduleBlock, day time.Time, loc *time.Location) []interval {
	day = day.In(loc)
	year, month, date := day.Date()
	weekday := isoWeekday(day)

	var result []interval

	for _, block := range schedule {
		if block.Weekday != weekday {
			continue
		}

		start, okStart := parseClock(block.StartTime)
		end, okEnd := parseClock(block.EndTime)
		if !okStart || !okEnd || start >= end {
			continue
		}

		// time.Date normaliza los minutos, así que 24:00 es la medianoche
		// del día siguiente.
		result = append(result, interval{
			start: time.Date(year, month, date, 0, start, 0, 0, loc),
			end:   time.Date(year, month, date, 0, end, 0, 0, loc),
		})
	}

	return mergeIntervals(result)
}

// mergeIntervals ordena los intervalos y une los que se tocan o se solapan.
func mergeIntervals(intervals []interval) []interval {
	if len(intervals) == 0 {
		return nil
	}

	sorted := append([]interval(nil), intervals...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].start.Before(sorted[j].start)
	})

	merged := []interval{sorted[0]}

	for _, current := range sorted[1:] {
		last := &merged[len(merged)-1]

		if current.start.After(last.end) {
			merged = append(merged, current)
			continue
		}

		if current.end.After(last.end) {
			last.end = current.end
		}
	}

	return merged
}

// subtractIntervals quita de free todo lo que cubre busy.
func subtractIntervals(free, busy []interval) []interval {
	busy = mergeIntervals(busy)

	var result []interval

	for _, current := range free {
		cursor := current.start

		for _, taken := range busy {
			if !taken.end.After(cursor) {
				continue
			}

			if !taken.start.Before(current.end) {
				break
			}

			if taken.start.After(cursor) {
				result = append(result, interval{start: cursor, end: taken.start})
			}

			cursor = taken.end
		}

		if cursor.Before(current.end) {
			result = append(result, interval{start: cursor, end: current.end})
		}
	}

	return result
}

// slotsIn divide cada intervalo libre en espacios consecutivos de la duración
// del servicio, empezando donde empieza el intervalo. Así no queda tiempo
// muerto después de una cita de otra duración.
func slotsIn(free []interval, duration time.Duration) []Slot {
	slots := []Slot{}

	if duration <= 0 {
		return slots
	}

	for _, current := range free {
		for start := current.start; !start.Add(duration).After(current.end); start = start.Add(duration) {
			slots = append(slots, Slot{StartsAt: start, EndsAt: start.Add(duration)})
		}
	}

	return slots
}

// covers indica si [start, end) cabe completo dentro de uno de los intervalos.
func covers(intervals []interval, start, end time.Time) bool {
	for _, current := range intervals {
		if !start.Before(current.start) && !end.After(current.end) {
			return true
		}
	}

	return false
}

// withinSchedule indica si una cita cae completa dentro del horario de
// atención (la mitad de I3). Mira también el día siguiente por si la cita
// cruza la medianoche entre dos bloques contiguos.
func withinSchedule(schedule []ScheduleBlock, start, end time.Time, loc *time.Location) bool {
	local := start.In(loc)

	working := workingIntervals(schedule, local, loc)
	working = append(working, workingIntervals(schedule, local.AddDate(0, 0, 1), loc)...)

	return covers(mergeIntervals(working), start, end)
}

// availableSlots calcula los espacios libres de un profesional en una fecha
// (RF7): el horario del día, menos los bloqueos y las citas vigentes, dividido
// según la duración del servicio. Es una función pura para poder probarla sin
// base de datos.
func availableSlots(
	schedule []ScheduleBlock,
	blocks []AgendaBlock,
	booked []Appointment,
	day time.Time,
	loc *time.Location,
	duration time.Duration,
) []Slot {
	busy := make([]interval, 0, len(blocks)+len(booked))

	for _, block := range blocks {
		busy = append(busy, interval{start: block.StartsAt, end: block.EndsAt})
	}

	for _, appointment := range booked {
		busy = append(busy, interval{start: appointment.StartsAt, end: appointment.EndsAt})
	}

	free := subtractIntervals(workingIntervals(schedule, day, loc), busy)

	slots := slotsIn(free, duration)
	for i := range slots {
		// Un espacio que empieza donde termina una cita hereda la zona de la
		// cita, que viene de la base en UTC. Todos se devuelven en la hora
		// local de la clínica.
		slots[i].StartsAt = slots[i].StartsAt.In(loc)
		slots[i].EndsAt = slots[i].EndsAt.In(loc)
	}

	return slots
}
