package tenancy

import (
	"context"
	"fmt"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implementa Store sobre tenancy_db.
type PostgresStore struct {
	pool *pgxpool.Pool
	tx   *database.TxManager
}

// NewPostgresStore devuelve el almacén de Tenancy.
func NewPostgresStore(pool *pgxpool.Pool, tx *database.TxManager) *PostgresStore {
	return &PostgresStore{pool: pool, tx: tx}
}

const clinicColumns = `id::text, identifier, name, timezone, status, created_at`

func scanClinic(row pgx.Row) (Clinic, error) {
	var clinic Clinic

	err := row.Scan(
		&clinic.ID,
		&clinic.Identifier,
		&clinic.Name,
		&clinic.Timezone,
		&clinic.Status,
		&clinic.CreatedAt,
	)

	return clinic, err
}

const userColumns = `
	id::text, COALESCE(clinic_id::text, ''), keycloak_subject, username, role_code, active, created_at
`

func scanUser(row pgx.Row) (User, error) {
	var user User
	var role string

	err := row.Scan(
		&user.ID,
		&user.ClinicID,
		&user.Subject,
		&user.Username,
		&role,
		&user.Active,
		&user.CreatedAt,
	)
	user.Role = auth.Role(role)

	return user, err
}

// InsertClinic registra una clínica.
func (s *PostgresStore) InsertClinic(ctx context.Context, clinic Clinic) (Clinic, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO clinics (identifier, name, timezone, status)
		VALUES ($1, $2, $3, $4)
		RETURNING `+clinicColumns,
		clinic.Identifier, clinic.Name, clinic.Timezone, clinic.Status,
	)

	created, err := scanClinic(row)
	if err != nil {
		return Clinic{}, database.MapError(err)
	}

	return created, nil
}

// ActivateClinic registra al primer administrador de la clínica y la activa en
// una sola transacción.
func (s *PostgresStore) ActivateClinic(ctx context.Context, clinicID string, admin User) (Clinic, User, error) {
	var clinic Clinic
	var user User

	err := s.tx.Run(ctx, database.TxOptions{}, func(ctx context.Context, tx *database.Tx) error {
		var err error

		user, err = insertUser(ctx, tx, admin)
		if err != nil {
			return err
		}

		clinic, err = scanClinic(tx.QueryRow(ctx, `
			UPDATE clinics
			SET status = 'active'
			WHERE id = $1 AND status = 'provisioning'
			RETURNING `+clinicColumns,
			clinicID,
		))
		if err != nil {
			return database.MapError(err)
		}

		return nil
	})
	if err != nil {
		return Clinic{}, User{}, err
	}

	return clinic, user, nil
}

// DeleteProvisioningClinic elimina una clínica cuya creación falló. Una
// clínica activa nunca se borra.
func (s *PostgresStore) DeleteProvisioningClinic(ctx context.Context, clinicID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM clinics
		WHERE id = $1 AND status = 'provisioning'
	`, clinicID)

	return database.MapError(err)
}

// ListClinics devuelve todas las clínicas ordenadas por identificador.
func (s *PostgresStore) ListClinics(ctx context.Context) ([]Clinic, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+clinicColumns+`
		FROM clinics
		ORDER BY identifier
	`)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	clinics := make([]Clinic, 0)
	for rows.Next() {
		clinic, err := scanClinic(rows)
		if err != nil {
			return nil, fmt.Errorf("scan clinic: %w", err)
		}

		clinics = append(clinics, clinic)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return clinics, nil
}

// InsertUser registra un usuario.
func (s *PostgresStore) InsertUser(ctx context.Context, user User) (User, error) {
	return insertUser(ctx, s.pool, user)
}

func insertUser(ctx context.Context, db database.DBTX, user User) (User, error) {
	var clinicID any
	if user.ClinicID != "" {
		clinicID = user.ClinicID
	}

	created, err := scanUser(db.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, username, clinic_id, role_code, active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+userColumns,
		user.Subject, user.Username, clinicID, string(user.Role), user.Active,
	))
	if err != nil {
		return User{}, database.MapError(err)
	}

	return created, nil
}

// GetUser devuelve un usuario de la clínica.
func (s *PostgresStore) GetUser(ctx context.Context, clinicID, userID string) (User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE clinic_id = $1 AND id = $2
	`, clinicID, userID))
	if err != nil {
		return User{}, database.MapError(err)
	}

	return user, nil
}

// FindUserBySubject devuelve el usuario con ese subject de Keycloak.
func (s *PostgresStore) FindUserBySubject(ctx context.Context, subject string) (User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE keycloak_subject = $1
	`, subject))
	if err != nil {
		return User{}, database.MapError(err)
	}

	return user, nil
}

// ListUsers devuelve los usuarios de la clínica ordenados por nombre de
// usuario.
func (s *PostgresStore) ListUsers(ctx context.Context, clinicID string) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE clinic_id = $1
		ORDER BY username
	`, clinicID)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return users, nil
}

// SetUserRole actualiza la copia local del rol de un usuario.
func (s *PostgresStore) SetUserRole(ctx context.Context, clinicID, userID string, role auth.Role) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		SET role_code = $3, updated_at = NOW()
		WHERE clinic_id = $1 AND id = $2 AND active
	`, clinicID, userID, string(role))
	if err != nil {
		return database.MapError(err)
	}

	if tag.RowsAffected() == 0 {
		return apperr.NotFound("user not found")
	}

	return nil
}

// DeactivateUser marca a un usuario como inactivo. Si ya lo estaba no hace
// nada, así que los reintentos tienen éxito.
func (s *PostgresStore) DeactivateUser(ctx context.Context, clinicID, userID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		SET active = FALSE, updated_at = NOW()
		WHERE clinic_id = $1 AND id = $2
	`, clinicID, userID)
	if err != nil {
		return database.MapError(err)
	}

	if tag.RowsAffected() == 0 {
		return apperr.NotFound("user not found")
	}

	return nil
}
