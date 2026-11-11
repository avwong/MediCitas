DROP TRIGGER IF EXISTS audit_log_immutable
ON audit_log;

DROP TABLE IF EXISTS audit_log;

DROP FUNCTION IF EXISTS reject_audit_log_mutation();
