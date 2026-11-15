package expediente

import (
	"context"
	"net/http"
	"net/url"

	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// AgendaClient implementa AppointmentVerifier con la ruta interna de Agenda
// que da el contexto de una cita para RF15.
//
// El bearer token del usuario se reenvía sin cambios, para que Agenda aplique
// su propia autenticación, el aislamiento entre clínicas y la autorización
// sobre el recurso.
type AgendaClient struct {
	client *svcclient.Client
}

// NewAgendaClient devuelve el adaptador de Agenda para RF15.
func NewAgendaClient(baseURL string) *AgendaClient {
	return &AgendaClient{
		client: svcclient.New(baseURL),
	}
}

// ClinicalNoteContext le pide a Agenda la información mínima de la cita que
// necesita RF15.
//
// Contrato que debe cumplir Agenda:
// GET /internal/appointments/{id}/clinical-note-context
func (c *AgendaClient) ClinicalNoteContext(
	ctx context.Context,
	bearerToken string,
	appointmentID string,
) (AppointmentContext, error) {
	var result AppointmentContext

	path := "/internal/appointments/" +
		url.PathEscape(appointmentID) +
		"/clinical-note-context"

	err := c.client.Do(
		ctx,
		http.MethodGet,
		path,
		bearerToken,
		nil,
		&result,
	)
	if err != nil {
		return AppointmentContext{}, err
	}

	return result, nil
}
