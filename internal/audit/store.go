package audit

import (
	"context"
	"fmt"

	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/outbox"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore guarda la bitácora central inmutable.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore devuelve el almacén de Auditoría.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Insert guarda un evento de forma idempotente.
//
// Con un relay de entrega "al menos una vez" los reenvíos son normales, así
// que event_id es la llave de idempotencia.
func (s *PostgresStore) Insert(
	ctx context.Context,
	event outbox.Event,
) error {
	_, err := s.pool.Exec(ctx, `
        INSERT INTO audit_log (
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
        ON CONFLICT (event_id) DO NOTHING
    `,
		event.EventID,
		event.ClinicID,
		event.ActorSubject,
		event.ActorUsername,
		event.Action,
		event.PatientID,
		event.ResourceType,
		event.ResourceID,
		event.OccurredAt,
		event.SourceService,
	)

	return database.MapError(err)
}

// List devuelve solo los registros de clinicID.
//
// La forma de la consulta es fija y todo valor que controla el usuario va como
// parámetro.
func (s *PostgresStore) List(
	ctx context.Context,
	clinicID string,
	filter Filter,
) ([]Entry, error) {
	var patientID any
	if filter.PatientID != "" {
		patientID = filter.PatientID
	}

	var from any
	if filter.From != nil {
		from = *filter.From
	}

	var to any
	if filter.To != nil {
		to = *filter.To
	}

	rows, err := s.pool.Query(ctx, `
        SELECT
            event_id::text,
            clinic_id::text,
            actor_subject,
            actor_username,
            action,
            patient_id::text,
            resource_type,
            resource_id::text,
            occurred_at,
            source_service,
            received_at
        FROM audit_log
        WHERE clinic_id = $1
          AND (
              $2 = ''
              OR actor_subject = $2
              OR actor_username = $2
          )
          AND (
              $3::uuid IS NULL
              OR patient_id = $3::uuid
          )
          AND (
              $4::timestamptz IS NULL
              OR occurred_at >= $4::timestamptz
          )
          AND (
              $5::timestamptz IS NULL
              OR occurred_at <= $5::timestamptz
          )
        ORDER BY occurred_at DESC, event_id DESC
        LIMIT $6
    `,
		clinicID,
		filter.User,
		patientID,
		from,
		to,
		filter.Limit,
	)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	entries := make([]Entry, 0)

	for rows.Next() {
		var entry Entry
		var action string

		if err := rows.Scan(
			&entry.EventID,
			&entry.ClinicID,
			&entry.ActorSubject,
			&entry.ActorUsername,
			&action,
			&entry.PatientID,
			&entry.ResourceType,
			&entry.ResourceID,
			&entry.OccurredAt,
			&entry.SourceService,
			&entry.ReceivedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}

		entry.Action = outbox.Action(action)
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return entries, nil
}
