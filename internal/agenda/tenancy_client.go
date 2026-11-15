package agenda

import (
	"context"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// TenancyDirectory implementa StaffDirectory con el servicio de Tenancy.
type TenancyDirectory struct {
	client *svcclient.Client
}

// NewTenancyDirectory devuelve un directorio que llama a Tenancy en baseURL.
func NewTenancyDirectory(baseURL string) *TenancyDirectory {
	return &TenancyDirectory{client: svcclient.New(baseURL)}
}

// GetUser pide un usuario con el token del propio actor. Tenancy aplica los
// mismos permisos y el mismo filtro de clínica que a cualquier petición, así
// que Agenda nunca puede ver por esta vía a un usuario de otra clínica.
func (d *TenancyDirectory) GetUser(ctx context.Context, actor auth.Actor, userID string) (StaffUser, error) {
	var user StaffUser

	err := d.client.Do(ctx, http.MethodGet, "/api/v1/users/"+userID, actor.Token, nil, &user)
	if err != nil {
		return StaffUser{}, err
	}

	return user, nil
}
