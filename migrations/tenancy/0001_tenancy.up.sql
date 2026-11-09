-- Tenancy: clinics and the users that work in them.
--
-- Keycloak holds the credentials and is the source of truth for each user's
-- role and clinic (both travel signed in the access token). These tables are
-- the platform's registry of clinics and of which identity belongs to which
-- clinic.

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
    identifier VARCHAR(64) NOT NULL UNIQUE
        CHECK (identifier ~ '^[a-z0-9][a-z0-9-]{1,63}$'),
    name VARCHAR(160) NOT NULL,
    timezone VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'provisioning'
        CHECK (status IN ('provisioning', 'active')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON COLUMN clinics.status IS
    'provisioning while the creation saga runs (agenda projection, administrator in Keycloak); active once it completes.';

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    keycloak_subject VARCHAR(64) NOT NULL UNIQUE,
    username VARCHAR(64) NOT NULL UNIQUE,
    clinic_id UUID REFERENCES clinics (id) ON DELETE RESTRICT,
    role_code VARCHAR(32) NOT NULL REFERENCES roles (code),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A user belongs to exactly one clinic, except the platform
    -- administrator, who belongs to none (RF2).
    CONSTRAINT users_tenant_role_check CHECK (
        (role_code = 'platform_admin' AND clinic_id IS NULL)
        OR
        (role_code <> 'platform_admin' AND clinic_id IS NOT NULL)
    )
);

COMMENT ON COLUMN users.role_code IS
    'Copy of the Keycloak realm role, which is the source of truth. Updated after Keycloak on every role change.';

-- Serves the clinic's user listing (RF2).
CREATE INDEX idx_users_clinic_username ON users (clinic_id, username);
