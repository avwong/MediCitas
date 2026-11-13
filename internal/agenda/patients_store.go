package agenda

import (
	"context"
	"strings"

	"github.com/avwong/MediCitas/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

// patientColumns son las columnas de un paciente completo
// La fecha se lee como texto AAAA-MM-DD para no depender de la zona horaria
// los datos de contacto sin valor (NULL) se devuelven como texto vacío
const patientColumns = `
	id::text, clinic_id::text, identification, first_name, last_name,
	to_char(birth_date, 'YYYY-MM-DD'),
	COALESCE(phone, ''), COALESCE(email, ''), COALESCE(address, ''),
	active, created_at, updated_at
`

// PostgresPatientStore implementa PatientStore sobre agenda_db
// No guarda el pool
// Cada método usa la conexión o transacción que le pasa el servicio
type PostgresPatientStore struct{}

// NewPostgresPatientStore devuelve el almacén de pacientes
func NewPostgresPatientStore() *PostgresPatientStore {
	return &PostgresPatientStore{}
}

// Se verifica en compilación que el almacén cumple la interfaz
var _ PatientStore = (*PostgresPatientStore)(nil)

func scanPatient(row pgx.Row) (Patient, error) {
	var patient Patient

	err := row.Scan(
		&patient.ID,
		&patient.ClinicID,
		&patient.Identification,
		&patient.FirstName,
		&patient.LastName,
		&patient.BirthDate,
		&patient.Phone,
		&patient.Email,
		&patient.Address,
		&patient.Active,
		&patient.CreatedAt,
		&patient.UpdatedAt,
	)

	return patient, err
}

// Insert crea un paciente en la clínica
// Los datos de contacto vacíos se guardan como NULL
// Una identificación repetida en la clínica produce un conflicto
// La restricción que lo detecta es patients_clinic_identification_unique
func (s *PostgresPatientStore) Insert(
	ctx context.Context,
	db database.DBTX,
	clinicID string,
	in NewPatient,
) (Patient, error) {
	row := db.QueryRow(ctx, `
		INSERT INTO patients (
			clinic_id, identification, first_name, last_name, birth_date,
			phone, email, address
		)
		VALUES ($1, $2, $3, $4, $5::text::date, NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''))
		RETURNING `+patientColumns,
		clinicID, in.Identification, in.FirstName, in.LastName, in.BirthDate,
		in.Phone, in.Email, in.Address,
	)

	patient, err := scanPatient(row)
	if err != nil {
		return Patient{}, database.MapError(err)
	}

	return patient, nil
}

// Update aplica solo los campos recibidos
// En cada columna, COALESCE conserva el valor actual si el parámetro es NULL
// En el contacto, NULL significa "no enviado" y se conserva el valor
// En el contacto, texto vacío significa "borrar" y la columna queda en NULL
// La condición clinic_id + id trata un paciente de otra clínica como inexistente
func (s *PostgresPatientStore) Update(
	ctx context.Context,
	db database.DBTX,
	clinicID, patientID string,
	in PatientUpdate,
) (Patient, error) {
	row := db.QueryRow(ctx, `
		UPDATE patients
		SET identification = COALESCE($3::text, identification),
		    first_name     = COALESCE($4::text, first_name),
		    last_name      = COALESCE($5::text, last_name),
		    birth_date     = COALESCE($6::text::date, birth_date),
		    phone   = CASE WHEN $7::text IS NULL THEN phone   ELSE NULLIF($7::text, '') END,
		    email   = CASE WHEN $8::text IS NULL THEN email   ELSE NULLIF($8::text, '') END,
		    address = CASE WHEN $9::text IS NULL THEN address ELSE NULLIF($9::text, '') END,
		    updated_at = NOW()
		WHERE clinic_id = $1 AND id = $2
		RETURNING `+patientColumns,
		clinicID, patientID,
		in.Identification, in.FirstName, in.LastName, in.BirthDate,
		in.Phone, in.Email, in.Address,
	)

	patient, err := scanPatient(row)
	if err != nil {
		return Patient{}, database.MapError(err)
	}

	return patient, nil
}

// Get devuelve un paciente de la clínica
func (s *PostgresPatientStore) Get(
	ctx context.Context,
	db database.DBTX,
	clinicID, patientID string,
) (Patient, error) {
	row := db.QueryRow(ctx, `
		SELECT `+patientColumns+`
		FROM patients
		WHERE clinic_id = $1 AND id = $2
	`, clinicID, patientID)

	patient, err := scanPatient(row)
	if err != nil {
		return Patient{}, database.MapError(err)
	}

	return patient, nil
}

// List devuelve los pacientes de la clínica ordenados por apellido y nombre
// Si hay texto de búsqueda, filtra por prefijo en identificación, nombre o apellido
// La búsqueda no distingue mayúsculas
// No devuelve datos de contacto
func (s *PostgresPatientStore) List(
	ctx context.Context,
	db database.DBTX,
	clinicID string,
	filter PatientFilter,
) ([]PatientSummary, error) {
	rows, err := db.Query(ctx, `
		SELECT id::text, identification, first_name, last_name,
		       to_char(birth_date, 'YYYY-MM-DD'), active
		FROM patients
		WHERE clinic_id = $1
		  AND ($2::text = ''
		       OR lower(identification) LIKE lower($2::text) || '%'
		       OR lower(first_name)     LIKE lower($2::text) || '%'
		       OR lower(last_name)      LIKE lower($2::text) || '%')
		ORDER BY lower(last_name), lower(first_name), id
		LIMIT $3
	`, clinicID, escapeLike(filter.Query), filter.Limit)
	if err != nil {
		return nil, database.MapError(err)
	}
	defer rows.Close()

	patients := make([]PatientSummary, 0)

	for rows.Next() {
		var patient PatientSummary

		err := rows.Scan(
			&patient.ID,
			&patient.Identification,
			&patient.FirstName,
			&patient.LastName,
			&patient.BirthDate,
			&patient.Active,
		)
		if err != nil {
			return nil, database.MapError(err)
		}

		patients = append(patients, patient)
	}

	if err := rows.Err(); err != nil {
		return nil, database.MapError(err)
	}

	return patients, nil
}

// escapeLike neutraliza los comodines de LIKE (\, % y _) en el texto de búsqueda
// Así se busca literalmente lo que escribió el usuario
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

	return replacer.Replace(value)
}
