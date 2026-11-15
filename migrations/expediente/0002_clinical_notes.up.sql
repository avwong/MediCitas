-- Expediente: notas clínicas (RF15).
--
-- Hay una nota por cita. appointment_id y patient_id apuntan a Agenda sin
-- llave foránea, porque viven en otra base de datos; Expediente los valida
-- con Agenda antes de insertar.

CREATE TABLE clinical_notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL,
    appointment_id UUID NOT NULL,
    patient_id UUID NOT NULL,
    practitioner_subject TEXT NOT NULL
        CHECK (length(btrim(practitioner_subject)) > 0),
    content TEXT NOT NULL
        CHECK (length(btrim(content)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT clinical_notes_one_per_appointment
        UNIQUE (clinic_id, appointment_id)
);

CREATE INDEX clinical_notes_owner_idx
    ON clinical_notes (
        clinic_id,
        practitioner_subject,
        created_at DESC
    );

-- RF15 solo crea y lee notas. La identidad de la aplicación no necesita
-- privilegios de modificación que la aplicación nunca usa.
GRANT SELECT, INSERT
ON clinical_notes
TO expediente_app;

GRANT SELECT
ON clinical_notes
TO expediente_readonly;

REVOKE UPDATE, DELETE, TRUNCATE
ON clinical_notes
FROM expediente_app;
