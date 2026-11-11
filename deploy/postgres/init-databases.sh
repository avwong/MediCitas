#!/bin/sh
# Creates one isolated database per service and three least-privilege roles:
#
#   <service>_owner
#       Owns the service database and runs migrations / DDL.
#
#   <service>_app
#       Runtime identity used by the Go service. It may perform only the DML
#       required by that service and cannot modify the schema.
#
#   <service>_readonly
#       Diagnostic/read-only identity. It may SELECT but cannot write or
#       modify the schema.
#
# audit_app is intentionally stricter: SELECT + INSERT only. UPDATE and
# DELETE are never granted, helping enforce invariant I6.
#
# The script is idempotent. It runs on every docker compose up and reapplies
# role attributes, passwords and grants.
set -eu

setup_service() {
  service="$1"
  owner_password="$2"
  app_password="$3"
  readonly_password="$4"
  app_privileges="$5"

  psql -v ON_ERROR_STOP=1 \
    -v owner="${service}_owner" \
    -v app="${service}_app" \
    -v readonly="${service}_readonly" \
    -v db="${service}_db" \
    -v owner_password="$owner_password" \
    -v app_password="$app_password" \
    -v readonly_password="$readonly_password" \
    -v app_privileges="$app_privileges" <<'SQL'

SELECT format('CREATE ROLE %I LOGIN', :'owner')
WHERE NOT EXISTS (
  SELECT FROM pg_roles WHERE rolname = :'owner'
)
\gexec

SELECT format('CREATE ROLE %I LOGIN', :'app')
WHERE NOT EXISTS (
  SELECT FROM pg_roles WHERE rolname = :'app'
)
\gexec

SELECT format('CREATE ROLE %I LOGIN', :'readonly')
WHERE NOT EXISTS (
  SELECT FROM pg_roles WHERE rolname = :'readonly'
)
\gexec

ALTER ROLE :"owner"
  WITH LOGIN
  NOSUPERUSER
  NOCREATEDB
  NOCREATEROLE
  NOREPLICATION
  NOBYPASSRLS
  PASSWORD :'owner_password';

ALTER ROLE :"app"
  WITH LOGIN
  NOSUPERUSER
  NOCREATEDB
  NOCREATEROLE
  NOREPLICATION
  NOBYPASSRLS
  PASSWORD :'app_password';

ALTER ROLE :"readonly"
  WITH LOGIN
  NOSUPERUSER
  NOCREATEDB
  NOCREATEROLE
  NOREPLICATION
  NOBYPASSRLS
  PASSWORD :'readonly_password';

SELECT format(
  'CREATE DATABASE %I OWNER %I',
  :'db',
  :'owner'
)
WHERE NOT EXISTS (
  SELECT FROM pg_database WHERE datname = :'db'
)
\gexec

-- Nobody receives database access implicitly.
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;

GRANT CONNECT, TEMPORARY
ON DATABASE :"db"
TO :"owner";

GRANT CONNECT
ON DATABASE :"db"
TO :"app";

GRANT CONNECT
ON DATABASE :"db"
TO :"readonly";

\connect :db

-- Nobody may create objects in public implicitly.
REVOKE ALL ON SCHEMA public FROM PUBLIC;

GRANT USAGE, CREATE
ON SCHEMA public
TO :"owner";

GRANT USAGE
ON SCHEMA public
TO :"app";

GRANT USAGE
ON SCHEMA public
TO :"readonly";

-- Reset previous grants before rebuilding the desired privilege model.
ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  REVOKE ALL ON TABLES FROM :"app";

ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  REVOKE ALL ON TABLES FROM :"readonly";

SELECT format(
  'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT %s ON TABLES TO %I',
  :'owner',
  :'app_privileges',
  :'app'
)
\gexec

ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  GRANT SELECT ON TABLES TO :"readonly";

-- Reapply the same policy to tables that already exist.
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM :"app";
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM :"readonly";

SELECT format(
  'GRANT %s ON ALL TABLES IN SCHEMA public TO %I',
  :'app_privileges',
  :'app'
)
\gexec

GRANT SELECT
ON ALL TABLES IN SCHEMA public
TO :"readonly";

-- Application roles may consume sequences but may not alter them.
ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  REVOKE ALL ON SEQUENCES FROM :"app";

ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  REVOKE ALL ON SEQUENCES FROM :"readonly";

ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO :"app";

REVOKE ALL
ON ALL SEQUENCES IN SCHEMA public
FROM :"app";

REVOKE ALL
ON ALL SEQUENCES IN SCHEMA public
FROM :"readonly";

GRANT USAGE, SELECT
ON ALL SEQUENCES IN SCHEMA public
TO :"app";

SQL
}

setup_service tenancy \
  "$TENANCY_DB_OWNER_PASSWORD" \
  "$TENANCY_DB_APP_PASSWORD" \
  "$TENANCY_DB_READONLY_PASSWORD" \
  "SELECT, INSERT, UPDATE, DELETE"

setup_service agenda \
  "$AGENDA_DB_OWNER_PASSWORD" \
  "$AGENDA_DB_APP_PASSWORD" \
  "$AGENDA_DB_READONLY_PASSWORD" \
  "SELECT, INSERT, UPDATE, DELETE"

setup_service expediente \
  "$EXPEDIENTE_DB_OWNER_PASSWORD" \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  "$EXPEDIENTE_DB_READONLY_PASSWORD" \
  "SELECT, INSERT, UPDATE, DELETE"

setup_service audit \
  "$AUDIT_DB_OWNER_PASSWORD" \
  "$AUDIT_DB_APP_PASSWORD" \
  "$AUDIT_DB_READONLY_PASSWORD" \
  "SELECT, INSERT"

# Reapply table-specific least privilege when db-init runs against an already
# migrated database. On a fresh database these tables do not exist yet and
# the migrations apply the same restrictions when they create them.
revoke_mutation_if_exists() {
  database="$1"
  role="$2"
  table="$3"

  psql \
    -v ON_ERROR_STOP=1 \
    -d "$database" \
    -v target_role="$role" \
    -v target_table="$table" <<'SQL'
SELECT format(
  'REVOKE UPDATE, DELETE, TRUNCATE ON TABLE %I FROM %I',
  :'target_table',
  :'target_role'
)
WHERE to_regclass(format('public.%I', :'target_table')) IS NOT NULL
\gexec
SQL
}

# Producer outboxes and delivery markers are append-oriented.
revoke_mutation_if_exists agenda_db agenda_app audit_outbox
revoke_mutation_if_exists agenda_db agenda_app audit_outbox_delivered

revoke_mutation_if_exists expediente_db expediente_app audit_outbox
revoke_mutation_if_exists expediente_db expediente_app audit_outbox_delivered

# RF15 defines creation and reading, not mutation or deletion of clinical
# notes.
revoke_mutation_if_exists expediente_db expediente_app clinical_notes

# Defense in depth for the central append-only audit log.
revoke_mutation_if_exists audit_db audit_app audit_log
# Appointment exclusion constraints require btree_gist. Infrastructure creates
# the extension so migration roles never require superuser privileges.
psql \
  -v ON_ERROR_STOP=1 \
  -d agenda_db \
  -c 'CREATE EXTENSION IF NOT EXISTS btree_gist;'

echo "databases and least-privilege roles are ready"
