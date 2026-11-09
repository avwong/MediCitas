package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/avwong/MediCitas/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type IdentityRepository struct {
	pool *pgxpool.Pool
}

func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{
		pool: pool,
	}
}

func (r *IdentityRepository) FindByExternalSubject(
	ctx context.Context,
	subject string,
) (tenancy.UserIdentity, error) {
	const query = `
		SELECT
			u.id::text,
			COALESCE(u.clinic_id::text, ''),
			u.role_code,
			u.active AND COALESCE(c.active, TRUE)
		FROM users u
		LEFT JOIN clinics c ON c.id = u.clinic_id
		WHERE u.keycloak_subject = $1
	`

	var identity tenancy.UserIdentity

	err := r.pool.QueryRow(
		ctx,
		query,
		subject,
	).Scan(
		&identity.UserID,
		&identity.ClinicID,
		&identity.Role,
		&identity.Active,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.UserIdentity{}, tenancy.ErrIdentityNotFound
	}

	if err != nil {
		return tenancy.UserIdentity{},
			fmt.Errorf("find identity by external subject: %w", err)
	}

	return identity, nil
}
