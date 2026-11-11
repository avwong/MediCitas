package audit

import (
	"context"
	"time"

	"github.com/avwong/MediCitas/internal/platform/outbox"
)

// Entry is one immutable audit-log record.
type Entry struct {
	EventID       string        `json:"event_id"`
	ClinicID      string        `json:"clinic_id"`
	ActorSubject  string        `json:"actor_subject"`
	ActorUsername string        `json:"actor_username,omitempty"`
	Action        outbox.Action `json:"action"`
	PatientID     string        `json:"patient_id"`
	ResourceType  string        `json:"resource_type"`
	ResourceID    string        `json:"resource_id"`
	OccurredAt    time.Time     `json:"occurred_at"`
	SourceService string        `json:"source_service"`
	ReceivedAt    time.Time     `json:"received_at"`
}

// Filter contains the RF19 filters.
type Filter struct {
	User      string
	PatientID string
	From      *time.Time
	To        *time.Time
	Limit     int
}

// Store persists and queries audit records.
type Store interface {
	Insert(ctx context.Context, event outbox.Event) error
	List(ctx context.Context, clinicID string, filter Filter) ([]Entry, error)
}
