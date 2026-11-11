package expediente

import (
	"context"
	"net/http"
	"net/url"

	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// AgendaClient implements AppointmentVerifier through Agenda's future RF15
// integration endpoint.
//
// The user's bearer token is forwarded unchanged so Agenda applies its own
// authentication, clinic isolation and resource-level authorization.
type AgendaClient struct {
	client *svcclient.Client
}

// NewAgendaClient returns the RF15 Agenda adapter.
func NewAgendaClient(baseURL string) *AgendaClient {
	return &AgendaClient{
		client: svcclient.New(baseURL),
	}
}

// ClinicalNoteContext asks Agenda for the minimal appointment information RF15
// needs.
//
// Contract for P2/P3:
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
