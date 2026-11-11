# PostgreSQL least-privilege model

MediCitas uses one PostgreSQL database per domain service and three database
profiles per service.

## Profiles

| Profile | Purpose | DDL | SELECT | INSERT | UPDATE | DELETE |
|---|---|---:|---:|---:|---:|---:|
| `<service>_owner` | migrations | yes | yes | yes | yes | yes |
| `<service>_app` | runtime service | no | yes | yes | yes | yes |
| `<service>_readonly` | read-only access | no | yes | no | no | no |
| `audit_app` | audit runtime | no | yes | yes | no | no |

Migration jobs connect as `<service>_owner`.

Runtime Go services connect as `<service>_app`.

Owner passwords are not provided to application containers.

## Service database isolation

Each service identity receives `CONNECT` only to its own database:

- `tenancy_*` -> `tenancy_db`
- `agenda_*` -> `agenda_db`
- `expediente_*` -> `expediente_db`
- `audit_*` -> `audit_db`

`PUBLIC` receives no privileges on the service databases.

A compromised service therefore cannot use its normal PostgreSQL credentials
to connect to another service database.

## Schema privileges

`PUBLIC` receives no privileges on the `public` schema.

Only `<service>_owner` receives `CREATE`.

Application and read-only identities receive only `USAGE`.

## Role attributes

All service roles explicitly use:

- `NOSUPERUSER`
- `NOCREATEDB`
- `NOCREATEROLE`
- `NOREPLICATION`
- `NOBYPASSRLS`

The application role therefore cannot bypass the future row-level security
layer.

## Read-only profile

`<service>_readonly` receives:

- database `CONNECT`;
- schema `USAGE`;
- table `SELECT`.

It receives no table write privileges, sequence privileges or schema creation
privileges.

## Audit database

`audit_app` intentionally receives only:

- `SELECT`;
- `INSERT`.

It does not receive `UPDATE` or `DELETE`.

The audit implementation adds another independent protection layer with
append-only triggers, but that belongs to the audit/outbox implementation.

## Domain roles

Mapping domain roles such as `clinic_admin`, `reception` and `practitioner`
onto PostgreSQL roles using `SET LOCAL ROLE` remains a separate architectural
decision.

This security layer establishes the migration, runtime and read-only profiles
without coupling it to that later decision.
