package tenancy

import (
	"context"
	"errors"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// AgendaClient implements AgendaProvisioner against the agenda's internal
// routes, authenticated as the tenancy service account.
type AgendaClient struct {
	client *svcclient.Client
	tokens *svcclient.ClientCredentials
}

// NewAgendaClient returns a client for the agenda service at baseURL.
func NewAgendaClient(baseURL string, tokens *svcclient.ClientCredentials) *AgendaClient {
	return &AgendaClient{client: svcclient.New(baseURL), tokens: tokens}
}

// ProvisionClinic creates or updates the clinic's projection in the agenda.
// The call is idempotent.
func (a *AgendaClient) ProvisionClinic(ctx context.Context, clinicID, timezone string) error {
	token, err := a.tokens.Token(ctx)
	if err != nil {
		return err
	}

	return a.client.Do(
		ctx,
		http.MethodPut,
		"/internal/clinics/"+clinicID,
		token,
		map[string]string{"timezone": timezone},
		nil,
	)
}

// DeprovisionClinic removes the clinic's projection from the agenda. A
// projection that is already gone counts as success.
func (a *AgendaClient) DeprovisionClinic(ctx context.Context, clinicID string) error {
	token, err := a.tokens.Token(ctx)
	if err != nil {
		return err
	}

	err = a.client.Do(ctx, http.MethodDelete, "/internal/clinics/"+clinicID, token, nil, nil)
	if errors.Is(err, apperr.ErrNotFound) {
		return nil
	}

	return err
}
