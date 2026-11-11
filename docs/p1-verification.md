# P1 verification and handoff

This document records the executable evidence for the platform, tenancy,
audit and expediente work owned by P1.

## One-command verification

PowerShell:

~~~powershell
powershell -ExecutionPolicy Bypass -File .\scripts\verify-p1.ps1
~~~

Linux, macOS or Git Bash:

~~~bash
sh ./scripts/verify-p1.sh
~~~

Neither command removes PostgreSQL volumes.

The verification workflow uses a conservative Compose parallelism limit of one
so it can also run on development machines with constrained Docker resources.

## Unit and static checks

~~~bash
go test ./...
go vet ./...
docker compose config --quiet
~~~

These checks validate package behavior, authorization rules, transaction and
outbox logic, RF15 rules and the Compose model.

## PostgreSQL privilege boundaries

~~~bash
docker compose --profile test run --rm db-security-test
~~~

The test verifies:

- owner, app and readonly identities have no elevated PostgreSQL attributes;
- application identities cannot create persistent or temporary tables;
- readonly identities cannot write;
- service database credentials cannot connect to another service database;
- sensitive append-oriented tables preserve SELECT/INSERT-only privileges
  even after the idempotent db-init job is executed again;
- expediente_app cannot UPDATE, DELETE or TRUNCATE clinical_notes.

Expected final line:

`All database privilege checks passed.`

## Audit invariants

~~~bash
docker compose --profile test run --rm audit-invariants-test
~~~

This verifies database-level append-only behavior and central idempotency.

Expected final line:

`All audit idempotency and immutability checks passed.`

## Real HTTP authentication and tenant isolation

~~~bash
docker compose --profile test run --build --rm p1-api-integration-test
~~~

This test uses the real Compose stack:

- Keycloak issues real user and service-account access tokens;
- each Go service validates the token;
- nginx routes only public API paths;
- missing and malformed tokens receive 401;
- users without the required permission receive 403;
- a forged X-Clinic-Id header or clinic_id query parameter cannot replace the
  signed clinic_id claim;
- Agenda resources from another clinic behave as not found;
- Tenancy users from another clinic behave as not found;
- Expediente routes are restricted to practitioners;
- nginx does not expose /internal routes even to a valid service account.

The two seeded clinics are intentionally used for the isolation checks.

Expected final line:

`All P1 API integration checks passed.`

## Real audit relay delivery

~~~bash
docker compose --profile test run --rm audit-relay-test
~~~

The test inserts synthetic events into the real Agenda and Expediente
outboxes.

The running relays must:

1. discover the pending event;
2. obtain their service-account token;
3. call the private Audit endpoint;
4. persist the event in audit_log;
5. record delivery separately in audit_outbox_delivered.

The synthetic relay events use a test-only clinic UUID that is not one of the
seeded user clinics, so they do not appear in normal RF19 demonstrations.

Expected final line:

`All audit relay delivery checks passed.`

## P1 handoff boundary

P1 provides:

- shared JWT/OIDC authentication and authorization;
- signed clinic_id tenant context;
- shared transaction manager;
- service-to-service HTTP support;
- database-per-service least privilege;
- transactional audit outbox and relay;
- central immutable Audit service and RF19;
- RF15 Expediente domain, persistence, authorization, audit producer and
  public API;
- executable infrastructure and integration verification.

Agenda still owns the future appointment implementation.

For RF15, Agenda must implement the contract documented in
`docs/expediente.md`:

`GET /internal/appointments/{id}/clinical-note-context`

Expediente deliberately does not create a temporary appointments model.

RF18 patient-contact production remains to be attached to RF14 when the
patient module is implemented.

The remaining Agenda requirements, RLS/domain-role work, concurrency, Redis
availability behavior and their dedicated tests remain outside this P1
handoff.
