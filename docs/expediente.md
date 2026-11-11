# Expediente and RF15

Expediente owns the most sensitive transactional data in MediCitas: clinical
notes.

The service has its own PostgreSQL database credentials and no other service
receives those credentials.

## RF15 flow

A practitioner creates a note through:

`POST /api/v1/clinical-notes`

with:

~~~json
{
  "appointment_id": "uuid",
  "content": "clinical note"
}
~~~

The caller does not provide clinic, patient or practitioner identifiers.

- clinic comes from the signed `clinic_id` token claim;
- practitioner comes from the signed token subject;
- patient comes from Agenda after the appointment is verified.

Only the `practitioner` role has `notes:own`.

## Agenda integration contract

Expediente depends on the `AppointmentVerifier` interface rather than on
Agenda's internal appointment implementation.

When appointments are implemented, Agenda must expose inside the Compose
network:

`GET /internal/appointments/{id}/clinical-note-context`

Expediente forwards the original user's bearer token.

A successful response has this shape:

~~~json
{
  "appointment_id": "uuid",
  "clinic_id": "uuid",
  "patient_id": "uuid",
  "practitioner_subject": "keycloak-subject",
  "status": "attended"
}
~~~

Agenda must derive the clinic from the authenticated token, never from query
parameters or headers supplied by the caller.

The endpoint must return only an appointment visible to that clinic.

`practitioner_subject` is the Keycloak subject associated with the
practitioner who attended the appointment.

The status value used by this contract for an attended appointment is:

`attended`

P2/P3 may implement their appointment tables and business logic independently
as long as this HTTP contract is preserved.

## Creation rules

Expediente accepts the note only when:

1. the caller has `notes:own`;
2. the appointment exists for the caller's clinic;
3. Agenda reports the appointment as `attended`;
4. the appointment was attended by the caller's Keycloak subject;
5. that appointment does not already have a clinical note.

Another practitioner's appointment is returned as not found rather than
revealing its existence.

There is one note per appointment.

## Reading

A practitioner reads their own note through:

`GET /api/v1/clinical-notes/{id}`

The database query is scoped simultaneously by:

- `clinic_id`;
- note id;
- `practitioner_subject`.

A note from another clinic or practitioner therefore behaves as not found.

## RF18 audit

Every successful clinical-note write creates:

`clinical_note.write`

Every successful clinical-note read creates:

`clinical_note.read`

The clinical operation and its audit event use the same PostgreSQL
transaction.

For a write:

~~~text
BEGIN
INSERT clinical_notes
INSERT audit_outbox
COMMIT
~~~

For a read:

~~~text
BEGIN
SELECT clinical_notes
INSERT audit_outbox
COMMIT
~~~

The read therefore intentionally uses a read-write transaction.

If the audit event cannot be recorded, the sensitive operation does not
complete successfully.

The existing outbox relay later delivers the event to Audit using client
credentials. The clinical note content itself is never copied into the audit
event.

## Current integration status

The RF15 domain, persistence, API, authorization boundary and audit producer
are implemented in Expediente.

The final end-to-end creation path depends on Agenda implementing the
`clinical-note-context` endpoint when appointments are added. No temporary
appointment table or fake Agenda domain is introduced by P1.
