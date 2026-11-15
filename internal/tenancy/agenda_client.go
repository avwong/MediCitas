package tenancy

import (
	"context"
	"errors"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// AgendaClient implementa AgendaProvisioner con las rutas internas de Agenda,
// autenticado como la cuenta de servicio de Tenancy.
type AgendaClient struct {
	client *svcclient.Client
	tokens *svcclient.ClientCredentials
}

// NewAgendaClient devuelve un cliente para el servicio de Agenda en baseURL.
func NewAgendaClient(baseURL string, tokens *svcclient.ClientCredentials) *AgendaClient {
	return &AgendaClient{client: svcclient.New(baseURL), tokens: tokens}
}

// ProvisionClinic crea o actualiza la proyección de la clínica en Agenda. La
// llamada es idempotente.
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

// DeprovisionClinic elimina la proyección de la clínica en Agenda. Si ya no
// existe, también cuenta como éxito.
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
