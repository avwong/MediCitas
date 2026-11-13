-- Agenda: pacientes de la clínica (RF14).
--
-- UNIQUE (clinic_id, id) permite que appointments referencie al paciente con
-- una llave foránea compuesta, como el resto de las tablas (I5).

CREATE TABLE patients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL REFERENCES clinics (id) ON DELETE RESTRICT,

    identification VARCHAR(40) NOT NULL
        CHECK (length(btrim(identification)) > 0),
    first_name VARCHAR(80) NOT NULL
        CHECK (length(btrim(first_name)) > 0),
    last_name VARCHAR(120) NOT NULL
        CHECK (length(btrim(last_name)) > 0),
    birth_date DATE NOT NULL
        CHECK (birth_date >= DATE '1900-01-01'),

    phone VARCHAR(30),
    email VARCHAR(254),
    address VARCHAR(300),

    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT patients_clinic_identification_unique
        UNIQUE (clinic_id, identification),
    CONSTRAINT patients_clinic_id_unique UNIQUE (clinic_id, id)
);

COMMENT ON TABLE patients IS
    'phone, email y address son datos de contacto: toda lectura o escritura se audita (RF18).';

-- Búsqueda por nombre dentro de una clínica. La búsqueda por identificación
-- la sirve el índice de patients_clinic_identification_unique.
CREATE INDEX idx_patients_clinic_name
    ON patients (clinic_id, lower(last_name), lower(first_name));
