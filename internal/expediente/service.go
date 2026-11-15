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

// AppointmentVerifier es lo único de Agenda de lo que depende Expediente.
//
// Agenda puede implementar las citas como necesite; Expediente solo depende de
// este contrato.
type AppointmentVerifier interface {
	ClinicalNoteContext(
		ctx context.Context,
		bearerToken string,
		appointmentID string,
	) (AppointmentContext, error)
}

// TxRunner lo implementa *database.TxManager.
type TxRunner interface {
	Run(
		ctx context.Context,
		opts database.TxOptions,
		fn func(context.Context, *database.Tx) error,
	) error
}

// AuditWriter lo implementa *outbox.Writer.
type AuditWriter interface {
	Append(
		ctx context.Context,
		db database.DBTX,
		event outbox.Event,
	) (outbox.Event, error)
}

// Service implementa RF15 y sus obligaciones de auditoría de RF18.
type Service struct {
	store        NoteStore
	appointments AppointmentVerifier
	tx           TxRunner
	audit        AuditWriter
}

// NewService devuelve el servicio de notas clínicas.
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

// Create asocia una nota clínica a una cita atendida.
//
// Se consulta a Agenda antes de abrir la transacción de Expediente. "Atendida"
// es un estado final (RF13), así que una vez que Agenda lo confirma ya no
// puede cambiar.
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

// Get devuelve una nota del profesional autenticado.
//
// El SELECT y el evento clinical_note.read ocurren en la misma transacción,
// que por eso es de lectura y escritura. La nota no se devuelve si su registro
// de auditoría no puede hacer commit.
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

	// Una respuesta remota nunca sustituye a la clínica del token firmado del
	// usuario.
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

	// Se responde 404 para no revelar que existe la cita de otro profesional.
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
