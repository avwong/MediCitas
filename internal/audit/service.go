package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/ids"
	"github.com/avwong/MediCitas/internal/platform/outbox"
)

// Service implements audit ingestion and RF19.
type Service struct {
	store Store
}

// NewService returns the audit use cases.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Ingest validates and stores one producer event.
func (s *Service) Ingest(
	ctx context.Context,
	event outbox.Event,
	source string,
) error {
	// The authenticated service account, not the request body, determines
	// the producer identity.
	event.SourceService = source

	normalized, err := outbox.NormalizeEvent(event)
	if err != nil {
		return fmt.Errorf(
			"%w: %v",
			apperr.Invalid("invalid audit event"),
			err,
		)
	}

	return s.store.Insert(ctx, normalized)
}

// List implements RF19 for the authenticated clinic administrator.
func (s *Service) List(
	ctx context.Context,
	actor auth.Actor,
	filter Filter,
) ([]Entry, error) {
	if !actor.Can(auth.PermAuditRead) || actor.ClinicID == "" {
		return nil, apperr.Forbidden("audit log access is forbidden")
	}

	filter.User = strings.TrimSpace(filter.User)

	if filter.PatientID != "" {
		patientID, ok := ids.Normalize(filter.PatientID)
		if !ok {
			return nil, apperr.Invalid("invalid patient_id")
		}

		filter.PatientID = patientID
	}

	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return nil, apperr.Invalid("from must not be after to")
	}

	switch {
	case filter.Limit == 0:
		filter.Limit = 100
	case filter.Limit < 1 || filter.Limit > 500:
		return nil, apperr.Invalid("limit must be between 1 and 500")
	}

	return s.store.List(ctx, actor.ClinicID, filter)
}
