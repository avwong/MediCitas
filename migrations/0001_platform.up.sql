CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE roles (
    code VARCHAR(32) PRIMARY KEY,
    description TEXT NOT NULL
);

INSERT INTO roles (code, description)
VALUES
    ('platform_admin', 'Administrator of the MediCitas platform'),
    ('clinic_admin', 'Administrator of a clinic'),
    ('reception', 'Clinic reception staff'),
    ('practitioner', 'Healthcare practitioner');

CREATE TABLE clinics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identifier VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(160) NOT NULL,
    timezone VARCHAR(64) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    keycloak_subject VARCHAR(128) NOT NULL UNIQUE,
    clinic_id UUID REFERENCES clinics(id) ON DELETE RESTRICT,
    role_code VARCHAR(32) NOT NULL REFERENCES roles(code),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT users_tenant_role_check CHECK (
        (role_code = 'platform_admin' AND clinic_id IS NULL)
        OR
        (role_code <> 'platform_admin' AND clinic_id IS NOT NULL)
    )
);

CREATE TABLE services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL REFERENCES clinics(id) ON DELETE RESTRICT,
    name VARCHAR(120) NOT NULL,
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes > 0),
    specialty VARCHAR(120) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT services_clinic_name_unique
        UNIQUE (clinic_id, name)
);

CREATE TABLE practitioners (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL REFERENCES clinics(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    display_name VARCHAR(160) NOT NULL,
    specialty VARCHAR(120) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE practitioner_services (
    practitioner_id UUID NOT NULL
        REFERENCES practitioners(id) ON DELETE CASCADE,

    service_id UUID NOT NULL
        REFERENCES services(id) ON DELETE RESTRICT,

    PRIMARY KEY (practitioner_id, service_id)
);

CREATE INDEX idx_users_clinic_id
    ON users(clinic_id);

CREATE INDEX idx_services_clinic_id
    ON services(clinic_id);

CREATE INDEX idx_practitioners_clinic_id
    ON practitioners(clinic_id);