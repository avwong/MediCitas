package expediente

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/avwong/MediCitas/internal/platform/ids"
	"github.com/avwong/MediCitas/internal/platform/outbox"
)

// AppointmentVerifier is the only Agenda capability Expediente depends on.
//
// P2/P3 may implement appointments however they need. Expediente only depends
// on this stable contract.
type AppointmentVerifier interface {
	ClinicalNoteContext(
		ctx context.Context,
		bearerToken string,
		appointmentID string,
	) (AppointmentContext, error)
}

// TxRunner is implemented by *database.TxManager.
type TxRunner interface {
	Run(
		ctx context.Context,
		opts database.TxOptions,
		fn func(context.Context, *database.Tx) error,
	) error
}

// AuditWriter is implemented by *outbox.Writer.
type AuditWriter interface {
	Append(
		ctx context.Context,
		db database.DBTX,
		event outbox.Event,
	) (outbox.Event, error)
}

// Service implements RF15 and its RF18 audit obligations.
type Service struct {
	store        NoteStore
	appointments AppointmentVerifier
	tx           TxRunner
	audit        AuditWriter
}

// NewService returns the clinical-note service.
func NewService(
	store NoteStore,
	appointments AppointmentVerifier,
	tx TxRunner,
	audit AuditWriter,
) *Service {
	return &Service{
		store:        store,
		appointments: appointments,
		tx:           tx,
		audit:        audit,
	}
}

// Create attaches one clinical note to an attended appointment.
//
// Agenda is consulted before opening the expediente transaction. Once Agenda
// confirms the appointment is attended, RF13 treats that status as final.
func (s *Service) Create(
	ctx context.Context,
	actor auth.Actor,
	in CreateNoteInput,
) (ClinicalNote, error) {
	if err := authorizePractitioner(actor); err != nil {
		return ClinicalNote{}, err
	}

	appointmentID, ok := ids.Normalize(in.AppointmentID)
	if !ok {
		return ClinicalNote{}, apperr.Invalid("invalid appointment_id")
	}

	if strings.TrimSpace(in.Content) == "" {
		return ClinicalNote{}, apperr.Invalid("content is required")
	}

	appointment, err := s.appointments.ClinicalNoteContext(
		ctx,
		actor.Token,
		appointmentID,
	)
	if err != nil {
		return ClinicalNote{}, err
	}

	appointment, err = validateAppointmentContext(
		appointment,
		appointmentID,
		actor,
	)
	if err != nil {
		return ClinicalNote{}, err
	}

	var note ClinicalNote

	err = s.tx.Run(
		ctx,
		database.TxOptions{ClinicID: actor.ClinicID},
		func(ctx context.Context, tx *database.Tx) error {
			note, err = s.store.Insert(
				ctx,
				tx,
				NewClinicalNote{
					ClinicID:            actor.ClinicID,
					AppointmentID:       appointment.AppointmentID,
					PatientID:           appointment.PatientID,
					PractitionerSubject: actor.Subject,
					Content:             in.Content,
				},
			)
			if err != nil {
				return err
			}

			_, err = s.audit.Append(
				ctx,
				tx,
				outbox.Event{
					ClinicID:      actor.ClinicID,
					ActorSubject:  actor.Subject,
					ActorUsername: actor.Username,
					Action:        outbox.ActionClinicalNoteWrite,
					PatientID:     note.PatientID,
					ResourceID:    note.ID,
				},
			)

			return err
		},
	)
	if err != nil {
		if errors.Is(err, apperr.ErrConflict) {
			return ClinicalNote{}, apperr.Conflict(
				"clinical note already exists for this appointment",
			)
		}

		return ClinicalNote{}, err
	}

	return note, nil
}

// Get returns one note owned by the authenticated practitioner.
//
// The SELECT and clinical_note.read audit event occur in the same read-write
// transaction. The note is not returned unless its audit record can commit.
func (s *Service) Get(
	ctx context.Context,
	actor auth.Actor,
	noteID string,
) (ClinicalNote, error) {
	if err := authorizePractitioner(actor); err != nil {
		return ClinicalNote{}, err
	}

	normalizedNoteID, ok := ids.Normalize(noteID)
	if !ok {
		return ClinicalNote{}, apperr.Invalid("invalid clinical note id")
	}

	var note ClinicalNote

	err := s.tx.Run(
		ctx,
		database.TxOptions{ClinicID: actor.ClinicID},
		func(ctx context.Context, tx *database.Tx) error {
			var err error

			note, err = s.store.GetOwned(
				ctx,
				tx,
				actor.ClinicID,
				normalizedNoteID,
				actor.Subject,
			)
			if err != nil {
				return err
			}

			_, err = s.audit.Append(
				ctx,
				tx,
				outbox.Event{
					ClinicID:      actor.ClinicID,
					ActorSubject:  actor.Subject,
					ActorUsername: actor.Username,
					Action:        outbox.ActionClinicalNoteRead,
					PatientID:     note.PatientID,
					ResourceID:    note.ID,
				},
			)

			return err
		},
	)
	if err != nil {
		return ClinicalNote{}, err
	}

	return note, nil
}

func authorizePractitioner(actor auth.Actor) error {
	if !actor.Can(auth.PermNotesOwn) ||
		strings.TrimSpace(actor.ClinicID) == "" ||
		strings.TrimSpace(actor.Subject) == "" {
		return apperr.Forbidden(
			"clinical notes are restricted to practitioners",
		)
	}

	return nil
}

func validateAppointmentContext(
	appointment AppointmentContext,
	expectedAppointmentID string,
	actor auth.Actor,
) (AppointmentContext, error) {
	appointmentID, ok := ids.Normalize(appointment.AppointmentID)
	if !ok || appointmentID != expectedAppointmentID {
		return AppointmentContext{}, fmt.Errorf(
			"%w: Agenda returned an invalid appointment identifier",
			apperr.ErrUnavailable,
		)
	}

	clinicID, ok := ids.Normalize(appointment.ClinicID)
	if !ok {
		return AppointmentContext{}, fmt.Errorf(
			"%w: Agenda returned an invalid clinic identifier",
			apperr.ErrUnavailable,
		)
	}

	// Never allow a remote response to override the clinic from the signed
	// user token.
	if clinicID != actor.ClinicID {
		return AppointmentContext{}, apperr.NotFound(
			"appointment not found",
		)
	}

	patientID, ok := ids.Normalize(appointment.PatientID)
	if !ok {
		return AppointmentContext{}, fmt.Errorf(
			"%w: Agenda returned an invalid patient identifier",
			apperr.ErrUnavailable,
		)
	}

	practitionerSubject := strings.TrimSpace(
		appointment.PractitionerSubject,
	)
	if practitionerSubject == "" {
		return AppointmentContext{}, fmt.Errorf(
			"%w: Agenda returned an invalid practitioner",
			apperr.ErrUnavailable,
		)
	}

	status := strings.ToLower(strings.TrimSpace(appointment.Status))

	if status != AppointmentStatusAttended {
		return AppointmentContext{}, apperr.Conflict(
			"appointment must be attended before adding a clinical note",
		)
	}

	// Return 404 instead of exposing another practitioner's appointment.
	if practitionerSubject != actor.Subject {
		return AppointmentContext{}, apperr.NotFound(
			"appointment not found",
		)
	}

	appointment.AppointmentID = appointmentID
	appointment.ClinicID = clinicID
	appointment.PatientID = patientID
	appointment.PractitionerSubject = practitionerSubject
	appointment.Status = status

	return appointment, nil
}
