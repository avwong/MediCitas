package outbox

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/avwong/MediCitas/internal/platform/database"
)

// Writer agrega eventos de auditoría inmutables al outbox local del productor.
type Writer struct {
	source string
	now    func() time.Time
}

// NewWriter crea el escritor del outbox de Agenda o de Expediente.
func NewWriter(source string) *Writer {
	return &Writer{
		source: source,
		now:    time.Now,
	}
}

// Append escribe el evento usando db. Si db es la transacción que hizo la
// operación auditada, las dos escrituras son atómicas.
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

	// El origen es una propiedad de este escritor; quien llama no puede
	// elegirlo.
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

	// RFC 4122, versión 4 y variante 1.
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
