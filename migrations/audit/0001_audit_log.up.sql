CREATE TABLE audit_log (
    event_id UUID PRIMARY KEY,
    clinic_id UUID NOT NULL,
    actor_subject TEXT NOT NULL CHECK (length(btrim(actor_subject)) > 0),
    actor_username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL CHECK (
        action IN (
            'patient_contact.read',
            'patient_contact.write',
            'clinical_note.read',
            'clinical_note.write'
        )
    ),
    patient_id UUID NOT NULL,
    resource_type TEXT NOT NULL CHECK (
        resource_type IN ('patient_contact', 'clinical_note')
    ),
    resource_id UUID NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    source_service TEXT NOT NULL CHECK (
        source_service IN ('agenda', 'expediente')
    ),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_clinic_time_idx
    ON audit_log (clinic_id, occurred_at DESC, event_id);

CREATE INDEX audit_log_clinic_actor_time_idx
    ON audit_log (clinic_id, actor_subject, occurred_at DESC);

CREATE INDEX audit_log_clinic_patient_time_idx
    ON audit_log (clinic_id, patient_id, occurred_at DESC);

CREATE OR REPLACE FUNCTION reject_audit_log_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER audit_log_immutable
BEFORE UPDATE OR DELETE ON audit_log
FOR EACH ROW
EXECUTE FUNCTION reject_audit_log_mutation();

-- audit_app is intentionally append-only.
REVOKE UPDATE, DELETE, TRUNCATE
ON audit_log
FROM audit_app;
