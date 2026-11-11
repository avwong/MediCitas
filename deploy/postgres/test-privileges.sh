#!/bin/sh
set -eu

: "${TENANCY_DB_OWNER_PASSWORD:?required}"
: "${TENANCY_DB_APP_PASSWORD:?required}"
: "${TENANCY_DB_READONLY_PASSWORD:?required}"

: "${AGENDA_DB_OWNER_PASSWORD:?required}"
: "${AGENDA_DB_APP_PASSWORD:?required}"
: "${AGENDA_DB_READONLY_PASSWORD:?required}"

: "${EXPEDIENTE_DB_OWNER_PASSWORD:?required}"
: "${EXPEDIENTE_DB_APP_PASSWORD:?required}"
: "${EXPEDIENTE_DB_READONLY_PASSWORD:?required}"

: "${AUDIT_DB_OWNER_PASSWORD:?required}"
: "${AUDIT_DB_APP_PASSWORD:?required}"
: "${AUDIT_DB_READONLY_PASSWORD:?required}"

run_psql() {
  user="$1"
  password="$2"
  database="$3"
  sql="$4"

  PGPASSWORD="$password" psql \
    -h postgres \
    -U "$user" \
    -d "$database" \
    -v ON_ERROR_STOP=1 \
    -X \
    -q \
    -c "$sql"
}

query_psql() {
  user="$1"
  password="$2"
  database="$3"
  sql="$4"

  PGPASSWORD="$password" psql \
    -h postgres \
    -U "$user" \
    -d "$database" \
    -v ON_ERROR_STOP=1 \
    -X \
    -q \
    -A \
    -t \
    -c "$sql"
}

expect_success() {
  label="$1"
  shift

  if "$@" >/tmp/db-security.out 2>&1; then
    echo "PASS: $label"
  else
    echo "FAIL: $label"
    cat /tmp/db-security.out
    exit 1
  fi
}

expect_failure() {
  label="$1"
  shift

  if "$@" >/tmp/db-security.out 2>&1; then
    echo "FAIL: $label (operation unexpectedly succeeded)"
    cat /tmp/db-security.out
    exit 1
  else
    echo "PASS: $label"
  fi
}

expect_value() {
  label="$1"
  expected="$2"
  shift 2

  if actual=$("$@" 2>/tmp/db-security.err); then
    if [ "$actual" = "$expected" ]; then
      echo "PASS: $label"
    else
      echo "FAIL: $label"
      echo "expected: $expected"
      echo "actual:   $actual"
      exit 1
    fi
  else
    echo "FAIL: $label"
    cat /tmp/db-security.err
    exit 1
  fi
}

check_role_flags() {
  role="$1"
  password="$2"
  database="$3"

  expect_value \
    "$role has no elevated PostgreSQL attributes" \
    "ok" \
    query_psql "$role" "$password" "$database" \
    "SELECT CASE
       WHEN NOT rolsuper
        AND NOT rolcreatedb
        AND NOT rolcreaterole
        AND NOT rolreplication
        AND NOT rolbypassrls
       THEN 'ok'
       ELSE 'bad'
     END
     FROM pg_roles
     WHERE rolname = current_user;"
}

check_service() {
  service="$1"
  owner_password="$2"
  app_password="$3"
  readonly_password="$4"
  app_can_modify="$5"

  database="${service}_db"
  owner="${service}_owner"
  app="${service}_app"
  readonly="${service}_readonly"

  echo ""
  echo "== $service =="

  check_role_flags "$owner" "$owner_password" "$database"
  check_role_flags "$app" "$app_password" "$database"
  check_role_flags "$readonly" "$readonly_password" "$database"

  expect_success \
    "$owner can perform DDL in its own database" \
    run_psql "$owner" "$owner_password" "$database" \
    "DROP TABLE IF EXISTS security_privilege_probe;
     CREATE TABLE security_privilege_probe (
       id integer PRIMARY KEY,
       value text NOT NULL
     );"

  expect_failure \
    "$app cannot create persistent tables" \
    run_psql "$app" "$app_password" "$database" \
    "CREATE TABLE app_must_not_create_tables (id integer);"

  expect_failure \
    "$app cannot create temporary tables" \
    run_psql "$app" "$app_password" "$database" \
    "CREATE TEMP TABLE app_must_not_create_temp_tables (id integer);"

  expect_failure \
    "$readonly cannot create tables" \
    run_psql "$readonly" "$readonly_password" "$database" \
    "CREATE TABLE readonly_must_not_create_tables (id integer);"

  expect_failure \
    "$app cannot assume the owner role" \
    run_psql "$app" "$app_password" "$database" \
    "SET ROLE ${owner};"

  expect_success \
    "$app can SELECT" \
    run_psql "$app" "$app_password" "$database" \
    "SELECT count(*) FROM security_privilege_probe;"

  expect_success \
    "$app can INSERT" \
    run_psql "$app" "$app_password" "$database" \
    "INSERT INTO security_privilege_probe(id, value)
     VALUES (1, 'created by app');"

  if [ "$app_can_modify" = "yes" ]; then

    expect_success \
      "$app can UPDATE where required" \
      run_psql "$app" "$app_password" "$database" \
      "UPDATE security_privilege_probe
       SET value = 'updated by app'
       WHERE id = 1;"

    expect_success \
      "$app can DELETE where required" \
      run_psql "$app" "$app_password" "$database" \
      "DELETE FROM security_privilege_probe
       WHERE id = 1;"

  else

    expect_failure \
      "$app cannot UPDATE" \
      run_psql "$app" "$app_password" "$database" \
      "UPDATE security_privilege_probe
       SET value = 'must fail'
       WHERE id = 1;"

    expect_failure \
      "$app cannot DELETE" \
      run_psql "$app" "$app_password" "$database" \
      "DELETE FROM security_privilege_probe
       WHERE id = 1;"

  fi

  expect_success \
    "$readonly can SELECT" \
    run_psql "$readonly" "$readonly_password" "$database" \
    "SELECT count(*) FROM security_privilege_probe;"

  expect_failure \
    "$readonly cannot INSERT" \
    run_psql "$readonly" "$readonly_password" "$database" \
    "INSERT INTO security_privilege_probe(id, value)
     VALUES (99, 'must fail');"

  expect_failure \
    "$readonly cannot UPDATE" \
    run_psql "$readonly" "$readonly_password" "$database" \
    "UPDATE security_privilege_probe
     SET value = 'must fail';"

  expect_failure \
    "$readonly cannot DELETE" \
    run_psql "$readonly" "$readonly_password" "$database" \
    "DELETE FROM security_privilege_probe;"

  expect_success \
    "$owner can clean up the probe table" \
    run_psql "$owner" "$owner_password" "$database" \
    "DROP TABLE security_privilege_probe;"
}

check_service \
  tenancy \
  "$TENANCY_DB_OWNER_PASSWORD" \
  "$TENANCY_DB_APP_PASSWORD" \
  "$TENANCY_DB_READONLY_PASSWORD" \
  yes

check_service \
  agenda \
  "$AGENDA_DB_OWNER_PASSWORD" \
  "$AGENDA_DB_APP_PASSWORD" \
  "$AGENDA_DB_READONLY_PASSWORD" \
  yes

check_service \
  expediente \
  "$EXPEDIENTE_DB_OWNER_PASSWORD" \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  "$EXPEDIENTE_DB_READONLY_PASSWORD" \
  yes

check_service \
  audit \
  "$AUDIT_DB_OWNER_PASSWORD" \
  "$AUDIT_DB_APP_PASSWORD" \
  "$AUDIT_DB_READONLY_PASSWORD" \
  no

echo ""
echo "== cross-service database isolation =="

expect_failure \
  "tenancy_app cannot connect to agenda_db" \
  run_psql tenancy_app "$TENANCY_DB_APP_PASSWORD" agenda_db "SELECT 1;"

expect_failure \
  "agenda_app cannot connect to tenancy_db" \
  run_psql agenda_app "$AGENDA_DB_APP_PASSWORD" tenancy_db "SELECT 1;"

expect_failure \
  "expediente_app cannot connect to audit_db" \
  run_psql expediente_app "$EXPEDIENTE_DB_APP_PASSWORD" audit_db "SELECT 1;"

expect_failure \
  "audit_app cannot connect to expediente_db" \
  run_psql audit_app "$AUDIT_DB_APP_PASSWORD" expediente_db "SELECT 1;"

expect_failure \
  "tenancy_owner cannot connect to agenda_db" \
  run_psql tenancy_owner "$TENANCY_DB_OWNER_PASSWORD" agenda_db "SELECT 1;"

expect_failure \
  "agenda_owner cannot connect to tenancy_db" \
  run_psql agenda_owner "$AGENDA_DB_OWNER_PASSWORD" tenancy_db "SELECT 1;"

expect_failure \
  "tenancy_readonly cannot connect to agenda_db" \
  run_psql tenancy_readonly "$TENANCY_DB_READONLY_PASSWORD" agenda_db "SELECT 1;"

expect_failure \
  "agenda_readonly cannot connect to tenancy_db" \
  run_psql agenda_readonly "$AGENDA_DB_READONLY_PASSWORD" tenancy_db "SELECT 1;"

echo ""
echo "All database privilege checks passed."
