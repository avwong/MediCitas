package tenancy

import (
	"context"
	"errors"
)

var (
	ErrIdentityNotFound  = errors.New("authenticated identity is not registered")
	ErrInactiveUser      = errors.New("authenticated user is inactive")
	ErrTenantUnavailable = errors.New("authenticated user has no clinic")
)

type UserIdentity struct {
	UserID   string
	ClinicID string
	Role     string
	Active   bool
}

type IdentityRepository interface {
	FindByExternalSubject(
		ctx context.Context,
		subject string,
	) (UserIdentity, error)
}

type Resolver struct {
	repository IdentityRepository
}

func NewResolver(repository IdentityRepository) *Resolver {
	return &Resolver{
		repository: repository,
	}
}

func (r *Resolver) Resolve(
	ctx context.Context,
	subject string,
) (Context, error) {
	identity, err := r.repository.FindByExternalSubject(ctx, subject)
	if err != nil {
		return Context{}, err
	}

	if !identity.Active {
		return Context{}, ErrInactiveUser
	}

	if identity.Role != "platform_admin" && identity.ClinicID == "" {
		return Context{}, ErrTenantUnavailable
	}

	return Context{
		UserID:   identity.UserID,
		ClinicID: identity.ClinicID,
		Role:     identity.Role,
	}, nil
}
