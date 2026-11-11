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

## Automated privilege verification

The privilege model is verified against a real PostgreSQL instance:

```bash
docker compose run --rm db-init
docker compose --profile test run --rm db-security-test
```

The integration test verifies both allowed and prohibited operations:

- migration owners can perform DDL only in their own database;
- runtime application roles cannot perform DDL;
- runtime application roles cannot create temporary tables;
- application roles cannot assume owner roles;
- normal application roles can perform their required DML;
- `audit_app` can insert and read but cannot update or delete;
- read-only identities can select but cannot write;
- service identities cannot connect to another service database;
- none of the service identities has superuser, database-creation,
  role-creation, replication or RLS-bypass privileges.

The script exits with a non-zero status immediately if a prohibited operation
unexpectedly succeeds.
