-- Agenda: local projection of clinics, service catalog and practitioners.
--
-- Every table carries clinic_id, and every relation between tables uses a
-- composite foreign key (clinic_id, id). The database therefore rejects any
-- row that points at a row of another clinic, whatever the application does
-- (I5).

CREATE TABLE clinics (
    id UUID PRIMARY KEY,
    timezone VARCHAR(64) NOT NULL,
    provisioned_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE clinics IS
    'Local projection of the clinics. The tenancy service is the source of truth and provisions each clinic here.';

CREATE TABLE services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL REFERENCES clinics (id) ON DELETE RESTRICT,
    name VARCHAR(120) NOT NULL,
    duration_minutes INTEGER NOT NULL
        CHECK (duration_minutes BETWEEN 1 AND 480),
    specialty VARCHAR(120) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT services_clinic_name_unique UNIQUE (clinic_id, name),
    CONSTRAINT services_clinic_id_unique UNIQUE (clinic_id, id)
);

CREATE TABLE practitioners (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL REFERENCES clinics (id) ON DELETE RESTRICT,
    user_id UUID NOT NULL UNIQUE,
    user_subject VARCHAR(64) NOT NULL UNIQUE,
    display_name VARCHAR(160) NOT NULL,
    specialty VARCHAR(120) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT practitioners_clinic_id_unique UNIQUE (clinic_id, id)
);

COMMENT ON COLUMN practitioners.user_id IS
    'User in the tenancy service. No foreign key: it lives in another database and is validated when the practitioner is registered.';

COMMENT ON COLUMN practitioners.user_subject IS
    'Keycloak subject of the user, used to recognize a practitioner from their token.';

CREATE TABLE practitioner_services (
    clinic_id UUID NOT NULL,
    practitioner_id UUID NOT NULL,
    service_id UUID NOT NULL,

    PRIMARY KEY (practitioner_id, service_id),

    CONSTRAINT practitioner_services_practitioner_fk
        FOREIGN KEY (clinic_id, practitioner_id)
        REFERENCES practitioners (clinic_id, id)
        ON DELETE CASCADE,

    CONSTRAINT practitioner_services_service_fk
        FOREIGN KEY (clinic_id, service_id)
        REFERENCES services (clinic_id, id)
        ON DELETE RESTRICT
);

-- Supports "which practitioners attend this service" (RF8). The lookups by
-- clinic on services and practitioners are served by the composite unique
-- constraints above, whose indexes start with clinic_id.
CREATE INDEX idx_practitioner_services_service
    ON practitioner_services (clinic_id, service_id);
