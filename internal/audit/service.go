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

// Service implementa la recepción de eventos de auditoría y RF19.
type Service struct {
	store Store
}

// NewService devuelve los casos de uso de Auditoría.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Ingest valida y guarda un evento de un productor.
func (s *Service) Ingest(
	ctx context.Context,
	event outbox.Event,
	source string,
) error {
	// Quién es el productor lo decide la cuenta de servicio autenticada, no el
	// cuerpo de la petición.
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

// List implementa RF19 para el administrador de clínica autenticado.
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
