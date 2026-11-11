package outbox

import (
	"context"
	"fmt"

	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore reads pending events and records successful deliveries.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates the relay persistence store.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Pending returns events that have not been recorded as delivered.
func (s *PostgresStore) Pending(
	ctx context.Context,
	limit int,
) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, `
        SELECT
            o.event_id::text,
            o.clinic_id::text,
            o.actor_subject,
            o.actor_username,
            o.action,
            o.patient_id::text,
            o.resource_type,
            o.resource_id::text,
            o.occurred_at,
            o.source_service
        FROM audit_outbox o
        LEFT JOIN audit_outbox_delivered d
            ON d.event_id = o.event_id
        WHERE d.event_id IS NULL
        ORDER BY o.occurred_at, o.event_id
        LIMIT $1
    `, limit)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	events := make([]Event, 0)

	for rows.Next() {
		var event Event
		var action string

		if err := rows.Scan(
			&event.EventID,
			&event.ClinicID,
			&event.ActorSubject,
			&event.ActorUsername,
			&action,
			&event.PatientID,
			&event.ResourceType,
			&event.ResourceID,
			&event.OccurredAt,
			&event.SourceService,
		); err != nil {
			return nil, fmt.Errorf("scan audit outbox event: %w", err)
		}

		event.Action = Action(action)

		normalized, err := NormalizeEvent(event)
		if err != nil {
			return nil, fmt.Errorf("invalid stored audit event %s: %w", event.EventID, err)
		}

		events = append(events, normalized)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return events, nil
}

// MarkDelivered records delivery without modifying the immutable outbox row.
func (s *PostgresStore) MarkDelivered(
	ctx context.Context,
	eventID string,
) error {
	_, err := s.pool.Exec(ctx, `
        INSERT INTO audit_outbox_delivered (event_id)
        VALUES ($1)
        ON CONFLICT (event_id) DO NOTHING
    `, eventID)

	return database.MapError(err)
}
