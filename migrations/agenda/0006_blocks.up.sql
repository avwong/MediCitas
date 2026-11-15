-- Agenda: bloqueos de agenda por vacaciones o permisos (RF6).
--
-- Un bloqueo es un intervalo de tiempo absoluto [starts_at, ends_at) en el
-- que el profesional no atiende. Retira la disponibilidad hacia adelante,
-- pero no cancela las citas ya reservadas que caigan dentro: esas citas se
-- listan como conflictos buscando las que se solapan con el bloqueo. Por eso
-- aquí no hay ninguna restricción contra appointments.

CREATE TABLE blocks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL,
    practitioner_id UUID NOT NULL,

    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    reason VARCHAR(200) NOT NULL CHECK (length(btrim(reason)) > 0),

    -- Subject de Keycloak de quien registró el bloqueo: el administrador de
    -- la clínica o el propio profesional.
    created_by VARCHAR(64) NOT NULL CHECK (length(btrim(created_by)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT blocks_time_order CHECK (starts_at < ends_at),

    CONSTRAINT blocks_clinic_id_unique UNIQUE (clinic_id, id),

    CONSTRAINT blocks_practitioner_fk
        FOREIGN KEY (clinic_id, practitioner_id)
        REFERENCES practitioners (clinic_id, id)
        ON DELETE CASCADE
);

COMMENT ON TABLE blocks IS
    'Bloqueos de agenda (RF6). Retiran disponibilidad pero no cancelan citas; dos bloqueos del mismo profesional pueden solaparse.';

-- La disponibilidad (RF7) y la reserva (I3) preguntan "qué bloqueos de este
-- profesional se solapan con este intervalo". Un índice GiST sobre el rango
-- responde esa pregunta con el operador &&; un B-tree sobre starts_at no
-- puede, porque un bloqueo largo empieza mucho antes del día consultado.
CREATE INDEX idx_blocks_practitioner_period
    ON blocks USING gist (practitioner_id, tstzrange(starts_at, ends_at));

-- Listado de los bloqueos de un profesional dentro de su clínica.
CREATE INDEX idx_blocks_practitioner
    ON blocks (clinic_id, practitioner_id, starts_at);
