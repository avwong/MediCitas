package outbox

import (
	"fmt"
	"strings"
	"time"

	"github.com/avwong/MediCitas/internal/platform/ids"
)

// Action identifica una de las operaciones sensibles que RF18 obliga a
// auditar.
type Action string

const (
	ActionPatientContactRead  Action = "patient_contact.read"
	ActionPatientContactWrite Action = "patient_contact.write"
	ActionClinicalNoteRead    Action = "clinical_note.read"
	ActionClinicalNoteWrite   Action = "clinical_note.write"
)

var actionResource = map[Action]string{
	ActionPatientContactRead:  "patient_contact",
	ActionPatientContactWrite: "patient_contact",
	ActionClinicalNoteRead:    "clinical_note",
	ActionClinicalNoteWrite:   "clinical_note",
}

var actionSource = map[Action]string{
	ActionPatientContactRead:  "agenda",
	ActionPatientContactWrite: "agenda",
	ActionClinicalNoteRead:    "expediente",
	ActionClinicalNoteWrite:   "expediente",
}

// Event es el evento de auditoría canónico que intercambian los productores y
// el servicio de Auditoría. A propósito lleva solo identificadores y
// metadatos, nunca el contenido de la nota clínica ni los valores de contacto.
type Event struct {
	EventID       string    `json:"event_id"`
	ClinicID      string    `json:"clinic_id"`
	ActorSubject  string    `json:"actor_subject"`
	ActorUsername string    `json:"actor_username,omitempty"`
	Action        Action    `json:"action"`
	PatientID     string    `json:"patient_id"`
	ResourceType  string    `json:"resource_type"`
	ResourceID    string    `json:"resource_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	SourceService string    `json:"source_service"`
}

// NormalizeEvent valida un evento y devuelve su representación canónica.
func NormalizeEvent(event Event) (Event, error) {
	var ok bool

	event.EventID, ok = ids.Normalize(event.EventID)
	if !ok {
		return Event{}, fmt.Errorf("invalid event_id")
	}

	event.ClinicID, ok = ids.Normalize(event.ClinicID)
	if !ok {
		return Event{}, fmt.Errorf("invalid clinic_id")
	}

	event.PatientID, ok = ids.Normalize(event.PatientID)
	if !ok {
		return Event{}, fmt.Errorf("invalid patient_id")
	}

	event.ResourceID, ok = ids.Normalize(event.ResourceID)
	if !ok {
		return Event{}, fmt.Errorf("invalid resource_id")
	}

	event.ActorSubject = strings.TrimSpace(event.ActorSubject)
	event.ActorUsername = strings.TrimSpace(event.ActorUsername)

	if event.ActorSubject == "" {
		return Event{}, fmt.Errorf("actor_subject is required")
	}

	expectedResource, ok := actionResource[event.Action]
	if !ok {
		return Event{}, fmt.Errorf("invalid audit action %q", event.Action)
	}

	// La acción determina el tipo de recurso, así que un productor no puede
	// falsificarlo.
	event.ResourceType = expectedResource

	event.SourceService = strings.ToLower(strings.TrimSpace(event.SourceService))

	expectedSource := actionSource[event.Action]
	if event.SourceService != expectedSource {
		return Event{}, fmt.Errorf(
			"action %q must originate from %s",
			event.Action,
			expectedSource,
		)
	}

	if event.OccurredAt.IsZero() {
		return Event{}, fmt.Errorf("occurred_at is required")
	}

	event.OccurredAt = event.OccurredAt.UTC()

	return event, nil
}
