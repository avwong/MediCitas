package outbox

import (
	"testing"
	"time"
)

func validEvent() Event {
	return Event{
		EventID:       "11111111-1111-4111-8111-111111111111",
		ClinicID:      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ActorSubject:  "user-subject",
		ActorUsername: "doctor",
		Action:        ActionClinicalNoteRead,
		PatientID:     "22222222-2222-4222-8222-222222222222",
		ResourceID:    "33333333-3333-4333-8333-333333333333",
		OccurredAt:    time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC),
		SourceService: "expediente",
	}
}

func TestNormalizeEventDerivesResourceType(t *testing.T) {
	event := validEvent()
	event.ResourceType = "forged"

	got, err := NormalizeEvent(event)
	if err != nil {
		t.Fatal(err)
	}

	if got.ResourceType != "clinical_note" {
		t.Fatalf("ResourceType = %q, want clinical_note", got.ResourceType)
	}
}

func TestNormalizeEventRejectsWrongSource(t *testing.T) {
	event := validEvent()
	event.SourceService = "agenda"

	if _, err := NormalizeEvent(event); err == nil {
		t.Fatal("expected clinical-note event from agenda to be rejected")
	}
}

func TestNormalizeEventRejectsInvalidPatient(t *testing.T) {
	event := validEvent()
	event.PatientID = "not-a-uuid"

	if _, err := NormalizeEvent(event); err == nil {
		t.Fatal("expected invalid patient id to be rejected")
	}
}
