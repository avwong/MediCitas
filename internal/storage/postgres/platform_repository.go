package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/avwong/MediCitas/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PlatformRepository struct {
	pool *pgxpool.Pool
}

func NewPlatformRepository(pool *pgxpool.Pool) *PlatformRepository {
	return &PlatformRepository{pool: pool}
}

func mapPostgresError(err error) error {
	var pgErr *pgconn.PgError

	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case "23505":
		// unique_violation
		return platform.ErrConflict

	case "23503":
		// foreign_key_violation
		return platform.ErrNotFound

	case "23514":
		// check_violation
		return platform.ErrInvalidInput

	default:
		return err
	}
}

func (r *PlatformRepository) CreateClinicWithAdmin(
	ctx context.Context,
	input platform.CreateClinicInput,
) (platform.Clinic, platform.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return platform.Clinic{}, platform.User{}, err
	}

	defer tx.Rollback(ctx)

	var clinic platform.Clinic

	err = tx.QueryRow(
		ctx,
		`
		INSERT INTO clinics (
			identifier,
			name,
			timezone
		)
		VALUES ($1, $2, $3)
		RETURNING
			id::text,
			identifier,
			name,
			timezone
		`,
		input.Identifier,
		input.Name,
		input.Timezone,
	).Scan(
		&clinic.ID,
		&clinic.Identifier,
		&clinic.Name,
		&clinic.Timezone,
	)

	if err != nil {
		return platform.Clinic{},
			platform.User{},
			mapPostgresError(err)
	}

	var user platform.User

	err = tx.QueryRow(
		ctx,
		`
		INSERT INTO users (
			keycloak_subject,
			clinic_id,
			role_code
		)
		VALUES ($1, $2, 'clinic_admin')
		RETURNING
			id::text,
			clinic_id::text,
			keycloak_subject,
			role_code,
			active
		`,
		input.AdminSubject,
		clinic.ID,
	).Scan(
		&user.ID,
		&user.ClinicID,
		&user.Subject,
		&user.Role,
		&user.Active,
	)

	if err != nil {
		return platform.Clinic{},
			platform.User{},
			mapPostgresError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return platform.Clinic{},
			platform.User{},
			fmt.Errorf("commit clinic creation: %w", err)
	}

	return clinic, user, nil
}

func (r *PlatformRepository) CreateUser(
	ctx context.Context,
	clinicID string,
	input platform.CreateUserInput,
) (platform.User, error) {
	var user platform.User

	err := r.pool.QueryRow(
		ctx,
		`
		INSERT INTO users (
			keycloak_subject,
			clinic_id,
			role_code
		)
		VALUES ($1, $2, $3)
		RETURNING
			id::text,
			clinic_id::text,
			keycloak_subject,
			role_code,
			active
		`,
		input.Subject,
		clinicID,
		input.Role,
	).Scan(
		&user.ID,
		&user.ClinicID,
		&user.Subject,
		&user.Role,
		&user.Active,
	)

	if err != nil {
		return platform.User{}, mapPostgresError(err)
	}

	return user, nil
}

func (r *PlatformRepository) DeactivateUser(
	ctx context.Context,
	clinicID string,
	userID string,
) error {
	tag, err := r.pool.Exec(
		ctx,
		`
		UPDATE users
		SET active = FALSE
		WHERE id = $1
		  AND clinic_id = $2
		  AND active = TRUE
		`,
		userID,
		clinicID,
	)

	if err != nil {
		return fmt.Errorf("deactivate user: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}

	return nil
}

func (r *PlatformRepository) UpdateUserRole(
	ctx context.Context,
	clinicID string,
	userID string,
	role string,
) error {
	tag, err := r.pool.Exec(
		ctx,
		`
		UPDATE users
		SET role_code = $3
		WHERE id = $1
		  AND clinic_id = $2
		  AND active = TRUE
		`,
		userID,
		clinicID,
		role,
	)

	if err != nil {
		return mapPostgresError(err)
	}

	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}

	return nil
}

func (r *PlatformRepository) CreateService(
	ctx context.Context,
	clinicID string,
	input platform.CreateServiceInput,
) (platform.Service, error) {
	var service platform.Service

	err := r.pool.QueryRow(
		ctx,
		`
		INSERT INTO services (
			clinic_id,
			name,
			duration_minutes,
			specialty
		)
		VALUES ($1, $2, $3, $4)
		RETURNING
			id::text,
			clinic_id::text,
			name,
			duration_minutes,
			specialty,
			active
		`,
		clinicID,
		input.Name,
		input.DurationMinutes,
		input.Specialty,
	).Scan(
		&service.ID,
		&service.ClinicID,
		&service.Name,
		&service.DurationMinutes,
		&service.Specialty,
		&service.Active,
	)

	if err != nil {
		return platform.Service{}, mapPostgresError(err)
	}

	return service, nil
}

func (r *PlatformRepository) CreatePractitioner(
	ctx context.Context,
	clinicID string,
	input platform.CreatePractitionerInput,
) (platform.Practitioner, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return platform.Practitioner{}, fmt.Errorf(
			"begin practitioner transaction: %w",
			err,
		)
	}

	defer tx.Rollback(ctx)

	var practitioner platform.Practitioner

	err = tx.QueryRow(
		ctx,
		`
		INSERT INTO practitioners (
			clinic_id,
			user_id,
			display_name,
			specialty
		)
		SELECT
			$1,
			u.id,
			$3,
			$4
		FROM users u
		WHERE u.id = $2
		  AND u.clinic_id = $1
		  AND u.role_code = 'practitioner'
		  AND u.active = TRUE
		RETURNING
			id::text,
			clinic_id::text,
			user_id::text,
			display_name,
			specialty
		`,
		clinicID,
		input.UserID,
		input.DisplayName,
		input.Specialty,
	).Scan(
		&practitioner.ID,
		&practitioner.ClinicID,
		&practitioner.UserID,
		&practitioner.DisplayName,
		&practitioner.Specialty,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Practitioner{}, platform.ErrNotFound
	}

	if err != nil {
		return platform.Practitioner{}, mapPostgresError(err)
	}

	for _, serviceID := range input.ServiceIDs {
		tag, err := tx.Exec(
			ctx,
			`
			INSERT INTO practitioner_services (
				practitioner_id,
				service_id
			)
			SELECT $1, s.id
			FROM services s
			WHERE s.id = $2
			  AND s.clinic_id = $3
			  AND s.active = TRUE
			`,
			practitioner.ID,
			serviceID,
			clinicID,
		)

		if err != nil {
			return platform.Practitioner{}, mapPostgresError(err)
		}

		if tag.RowsAffected() == 0 {
			return platform.Practitioner{}, platform.ErrNotFound
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return platform.Practitioner{},
			fmt.Errorf("commit practitioner creation: %w", err)
	}

	practitioner.ServiceIDs = input.ServiceIDs

	return practitioner, nil
}
