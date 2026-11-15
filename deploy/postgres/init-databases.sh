#!/bin/sh
# Crea una base de datos aislada por servicio y tres roles de menor privilegio:
#
#   <servicio>_owner
#       Dueño de la base del servicio. Ejecuta las migraciones (DDL).
#
#   <servicio>_app
#       Identidad con la que se conecta el servicio en Go. Solo puede hacer el
#       DML que el servicio necesita y no puede modificar el esquema.
#
#   <servicio>_readonly
#       Identidad de diagnóstico. Puede hacer SELECT, pero no escribir ni
#       modificar el esquema.
#
# audit_app es más estricto a propósito: solo SELECT e INSERT. Nunca recibe
# UPDATE ni DELETE, lo que ayuda a sostener el invariante I6.
#
# El script es idempotente: corre en cada docker compose up y vuelve a aplicar
# los atributos de los roles, las contraseñas y los permisos.
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

-- Nadie recibe acceso a la base de forma implícita.
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

-- Nadie puede crear objetos en public de forma implícita.
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

-- Se quitan los permisos anteriores antes de reconstruir el modelo deseado.
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

-- La misma política se aplica a las tablas que ya existen.
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

-- Los roles de aplicación pueden usar las secuencias, pero no alterarlas.
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

# Vuelve a aplicar el menor privilegio por tabla cuando db-init corre sobre una
# base ya migrada. En una base nueva estas tablas todavía no existen, y las
# migraciones aplican las mismas restricciones al crearlas.
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

# Los outbox de los productores y sus marcas de entrega solo admiten
# inserciones.
revoke_mutation_if_exists agenda_db agenda_app audit_outbox
revoke_mutation_if_exists agenda_db agenda_app audit_outbox_delivered

revoke_mutation_if_exists expediente_db expediente_app audit_outbox
revoke_mutation_if_exists expediente_db expediente_app audit_outbox_delivered

# RF15 define crear y leer notas clínicas, no modificarlas ni borrarlas.
revoke_mutation_if_exists expediente_db expediente_app clinical_notes

# Defensa en profundidad para la bitácora central, que solo admite inserciones.
revoke_mutation_if_exists audit_db audit_app audit_log

# Las restricciones de exclusión de las citas necesitan btree_gist. La
# extensión se crea aquí, con la infraestructura, para que los roles de
# migración nunca necesiten ser superusuario.
psql \
  -v ON_ERROR_STOP=1 \
  -d agenda_db \
  -c 'CREATE EXTENSION IF NOT EXISTS btree_gist;'

echo "databases and least-privilege roles are ready"
