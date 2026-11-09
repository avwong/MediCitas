#!/bin/sh
# Creates one database per service and two roles for each:
#
#   <service>_owner  owns the database and runs the migrations (DDL).
#   <service>_app    is the role the service connects with: it can only read
#                    and write rows, never change the schema, and it cannot
#                    connect to any other service's database.
#
# The audit application role only gets SELECT and INSERT, so the audit log is
# append-only for the application at the engine level (I6).
#
# The script is idempotent: it runs on every `docker compose up` and also
# re-applies passwords and grants, so changing .env takes effect without
# deleting the volume. (Scripts in docker-entrypoint-initdb.d only run when
# the volume is empty, which is why this is a separate job.)
set -eu

setup_service() {
  service="$1"
  owner_password="$2"
  app_password="$3"
  app_privileges="$4"

  psql -v ON_ERROR_STOP=1 \
    -v owner="${service}_owner" \
    -v app="${service}_app" \
    -v db="${service}_db" \
    -v owner_password="$owner_password" \
    -v app_password="$app_password" \
    -v app_privileges="$app_privileges" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN', :'owner')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'owner')
\gexec

SELECT format('CREATE ROLE %I LOGIN', :'app')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'app')
\gexec

ALTER ROLE :"owner" WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'owner_password';
ALTER ROLE :"app" WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'app_password';

SELECT format('CREATE DATABASE %I OWNER %I', :'db', :'owner')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db')
\gexec

REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE :"db" TO :"owner";
GRANT CONNECT ON DATABASE :"db" TO :"app";

\connect :db

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO :"app";

SELECT format(
  'ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT %s ON TABLES TO %I',
  :'owner', :'app_privileges', :'app'
)
\gexec

SELECT format(
  'GRANT %s ON ALL TABLES IN SCHEMA public TO %I',
  :'app_privileges', :'app'
)
\gexec

ALTER DEFAULT PRIVILEGES FOR ROLE :"owner" IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO :"app";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO :"app";
SQL
}

setup_service tenancy \
  "$TENANCY_DB_OWNER_PASSWORD" "$TENANCY_DB_APP_PASSWORD" \
  "SELECT, INSERT, UPDATE, DELETE"

setup_service agenda \
  "$AGENDA_DB_OWNER_PASSWORD" "$AGENDA_DB_APP_PASSWORD" \
  "SELECT, INSERT, UPDATE, DELETE"

setup_service expediente \
  "$EXPEDIENTE_DB_OWNER_PASSWORD" "$EXPEDIENTE_DB_APP_PASSWORD" \
  "SELECT, INSERT, UPDATE, DELETE"

setup_service audit \
  "$AUDIT_DB_OWNER_PASSWORD" "$AUDIT_DB_APP_PASSWORD" \
  "SELECT, INSERT"

# Exclusion constraints on appointments (I1, I2) combine equality on UUIDs
# with overlap on time ranges, which needs btree_gist. Extensions are created
# by the superuser here so the owner role does not need extra privileges.
psql -v ON_ERROR_STOP=1 -d agenda_db -c 'CREATE EXTENSION IF NOT EXISTS btree_gist;'

echo "databases and roles are ready"
