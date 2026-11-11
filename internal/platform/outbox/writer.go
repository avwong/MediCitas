package outbox

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/avwong/MediCitas/internal/platform/database"
)

// Writer appends immutable audit events to the producer's local outbox.
type Writer struct {
	source string
	now    func() time.Time
}

// NewWriter creates an outbox writer for agenda or expediente.
func NewWriter(source string) *Writer {
	return &Writer{
		source: source,
		now:    time.Now,
	}
}

// Append writes event using db. Passing the transaction that performed the
// audited operation makes both writes atomic.
func (w *Writer) Append(
	ctx context.Context,
	db database.DBTX,
	event Event,
) (Event, error) {
	if event.EventID == "" {
		id, err := newUUID()
		if err != nil {
			return Event{}, fmt.Errorf("generate audit event id: %w", err)
		}

		event.EventID = id
	}

	if event.OccurredAt.IsZero() {
		event.OccurredAt = w.now().UTC()
	}

	// Source is a property of this writer, not caller-controlled payload.
	event.SourceService = w.source

	normalized, err := NormalizeEvent(event)
	if err != nil {
		return Event{}, err
	}

	_, err = db.Exec(ctx, `
        INSERT INTO audit_outbox (
            event_id,
            clinic_id,
            actor_subject,
            actor_username,
            action,
            patient_id,
            resource_type,
            resource_id,
            occurred_at,
            source_service
        )
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    `,
		normalized.EventID,
		normalized.ClinicID,
		normalized.ActorSubject,
		normalized.ActorUsername,
		normalized.Action,
		normalized.PatientID,
		normalized.ResourceType,
		normalized.ResourceID,
		normalized.OccurredAt,
		normalized.SourceService,
	)
	if err != nil {
		return Event{}, database.MapError(err)
	}

	return normalized, nil
}

func newUUID() (string, error) {
	var value [16]byte

	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}

	// RFC 4122 version 4 / variant 1.
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
