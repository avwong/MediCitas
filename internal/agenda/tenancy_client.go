package agenda

import (
	"context"
	"net/http"

	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// TenancyDirectory implements StaffDirectory against the tenancy service.
type TenancyDirectory struct {
	client *svcclient.Client
}

// NewTenancyDirectory returns a directory that calls tenancy at baseURL.
func NewTenancyDirectory(baseURL string) *TenancyDirectory {
	return &TenancyDirectory{client: svcclient.New(baseURL)}
}

// GetUser fetches a user with the actor's own token. Tenancy applies the
// same permission and clinic checks it applies to any request, so the agenda
// can never see a user of another clinic through this call.
func (d *TenancyDirectory) GetUser(ctx context.Context, actor auth.Actor, userID string) (StaffUser, error) {
	var user StaffUser

	err := d.client.Do(ctx, http.MethodGet, "/api/v1/users/"+userID, actor.Token, nil, &user)
	if err != nil {
		return StaffUser{}, err
	}

	return user, nil
}
