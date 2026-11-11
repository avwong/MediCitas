CREATE TABLE audit_outbox (
    event_id UUID PRIMARY KEY,
    clinic_id UUID NOT NULL,
    actor_subject TEXT NOT NULL CHECK (length(btrim(actor_subject)) > 0),
    actor_username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL CHECK (
        action IN ('patient_contact.read', 'patient_contact.write')
    ),
    patient_id UUID NOT NULL,
    resource_type TEXT NOT NULL CHECK (resource_type = 'patient_contact'),
    resource_id UUID NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    source_service TEXT NOT NULL CHECK (source_service = 'agenda'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_outbox_delivered (
    event_id UUID PRIMARY KEY
        REFERENCES audit_outbox(event_id)
        ON DELETE RESTRICT,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_outbox_pending_idx
    ON audit_outbox (occurred_at, event_id);

CREATE OR REPLACE FUNCTION reject_audit_outbox_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_outbox is append-only'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER audit_outbox_immutable
BEFORE UPDATE OR DELETE ON audit_outbox
FOR EACH ROW
EXECUTE FUNCTION reject_audit_outbox_mutation();

-- Default privileges give agenda_app normal Agenda DML. The outbox is stricter.
REVOKE UPDATE, DELETE, TRUNCATE
ON audit_outbox
FROM agenda_app;

REVOKE UPDATE, DELETE, TRUNCATE
ON audit_outbox_delivered
FROM agenda_app;
