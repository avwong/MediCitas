DROP TABLE IF EXISTS audit_outbox_delivered;

DROP TRIGGER IF EXISTS audit_outbox_immutable
ON audit_outbox;

DROP TABLE IF EXISTS audit_outbox;

DROP FUNCTION IF EXISTS reject_audit_outbox_mutation();
