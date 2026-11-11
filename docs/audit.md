# Audit and transactional outbox

MediCitas centralizes RF18 audit records in the Audit service while preserving
the atomicity of each sensitive operation at its source.

## Events

RF18 covers every read and write of:

- patient contact data in Agenda;
- clinical notes in Expediente.

An audit event records:

- clinic;
- authenticated subject and username;
- action;
- patient;
- affected resource;
- occurrence time;
- producer service.

Sensitive contact values and clinical-note contents are never copied into the
audit event.

## Transactional outbox

Agenda and Expediente append an event to their local `audit_outbox` inside the
same PostgreSQL transaction as the sensitive operation.

If that transaction rolls back, neither the business operation nor its audit
event remains committed.

The outbox is append-only. Runtime application roles cannot update, delete or
truncate its rows. A PostgreSQL trigger independently rejects UPDATE and
DELETE.

The relay never changes an outbox row. Successful deliveries are recorded in
the separate `audit_outbox_delivered` table.

## Delivery semantics

The relay obtains a service-account access token through OAuth 2.0 client
credentials and sends pending events to:

`POST /internal/audit/events`

Delivery is at least once. A producer may resend an event when Audit accepted
it but the producer failed before recording the delivery locally.

Audit therefore uses `event_id` as an idempotency key and stores events with:

`ON CONFLICT (event_id) DO NOTHING`

One logical event therefore produces at most one row in the central log.

## RF19

Clinic administrators query:

`GET /api/v1/audit`

Optional filters:

- `user`: authenticated subject or username;
- `patient_id`: patient UUID;
- `from`: inclusive RFC3339 timestamp;
- `to`: inclusive RFC3339 timestamp;
- `limit`: 1 to 500, default 100.

The clinic is never supplied by the request. It comes from the authenticated
actor's signed `clinic_id` claim.

Receptionists, practitioners and platform administrators cannot query RF19.

## I6

The central `audit_log` is protected by two independent database controls:

1. `audit_app` receives the DML required by Audit but no UPDATE, DELETE or
   TRUNCATE permission on `audit_log`.
2. A PostgreSQL trigger rejects UPDATE and DELETE even when attempted with the
   migration-owner identity.

Agenda and Expediente apply the same append-only protection to
`audit_outbox`.

## Verification

Unit tests:

```bash
go test ./...
```

Database-role boundaries:

```bash
docker compose --profile test run --rm db-security-test
```

Audit invariants and idempotency:

```bash
docker compose --profile test run --rm audit-invariants-test
```

The audit invariant test verifies that producer outboxes and the central log
cannot be mutated by the runtime application identities and that redelivering
one event produces one central audit row.
