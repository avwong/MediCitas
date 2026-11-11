#!/bin/sh
set -eu

HOST="${PGHOST:-postgres}"

new_uuid() {
  cat /proc/sys/kernel/random/uuid
}

psql_as() {
  password="$1"
  user="$2"
  database="$3"
  shift 3

  PGPASSWORD="$password" \
    psql \
      -X \
      -v ON_ERROR_STOP=1 \
      -h "$HOST" \
      -U "$user" \
      -d "$database" \
      "$@"
}

expect_fail_sql() {
  description="$1"
  password="$2"
  user="$3"
  database="$4"
  sql="$5"

  if PGPASSWORD="$password" \
    psql \
      -X \
      -v ON_ERROR_STOP=1 \
      -h "$HOST" \
      -U "$user" \
      -d "$database" \
      -c "$sql" \
      >/tmp/audit-test.out 2>&1
  then
    echo "FAIL: $description unexpectedly succeeded"
    cat /tmp/audit-test.out
    exit 1
  fi

  echo "ok - $description"
}

echo "== Agenda audit_outbox =="

agenda_event="$(new_uuid)"
agenda_patient="$(new_uuid)"

psql_as \
  "$AGENDA_DB_APP_PASSWORD" \
  agenda_app \
  agenda_db \
  -c "
    INSERT INTO audit_outbox (
      event_id,
      clinic_id,
      actor_subject,
      actor_username,
      action,
      patient_id,
      resource_type,
      resource_id,
      occurred_at,
      source_service
    )
    VALUES (
      '$agenda_event',
      'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
      'test-subject',
      'test-user',
      'patient_contact.read',
      '$agenda_patient',
      'patient_contact',
      '$agenda_patient',
      now(),
      'agenda'
    );
  " >/dev/null

echo "ok - agenda_app can append outbox events"

expect_fail_sql \
  "agenda_app cannot UPDATE audit_outbox" \
  "$AGENDA_DB_APP_PASSWORD" \
  agenda_app \
  agenda_db \
  "UPDATE audit_outbox
   SET actor_username = 'tampered'
   WHERE event_id = '$agenda_event';"

expect_fail_sql \
  "agenda_app cannot DELETE audit_outbox" \
  "$AGENDA_DB_APP_PASSWORD" \
  agenda_app \
  agenda_db \
  "DELETE FROM audit_outbox
   WHERE event_id = '$agenda_event';"

expect_fail_sql \
  "agenda_app cannot TRUNCATE audit_outbox" \
  "$AGENDA_DB_APP_PASSWORD" \
  agenda_app \
  agenda_db \
  "TRUNCATE audit_outbox;"

expect_fail_sql \
  "agenda_owner trigger rejects UPDATE audit_outbox" \
  "$AGENDA_DB_OWNER_PASSWORD" \
  agenda_owner \
  agenda_db \
  "UPDATE audit_outbox
   SET actor_username = 'owner-tampered'
   WHERE event_id = '$agenda_event';"

expect_fail_sql \
  "agenda_owner trigger rejects DELETE audit_outbox" \
  "$AGENDA_DB_OWNER_PASSWORD" \
  agenda_owner \
  agenda_db \
  "DELETE FROM audit_outbox
   WHERE event_id = '$agenda_event';"

psql_as \
  "$AGENDA_DB_APP_PASSWORD" \
  agenda_app \
  agenda_db \
  -c "
    INSERT INTO audit_outbox_delivered (event_id)
    VALUES ('$agenda_event');
  " >/dev/null

echo "ok - agenda relay can record delivery separately"

echo "== Expediente audit_outbox =="

exp_event="$(new_uuid)"
exp_patient="$(new_uuid)"
exp_note="$(new_uuid)"

psql_as \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  expediente_app \
  expediente_db \
  -c "
    INSERT INTO audit_outbox (
      event_id,
      clinic_id,
      actor_subject,
      actor_username,
      action,
      patient_id,
      resource_type,
      resource_id,
      occurred_at,
      source_service
    )
    VALUES (
      '$exp_event',
      'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
      'doctor-subject',
      'doctor',
      'clinical_note.write',
      '$exp_patient',
      'clinical_note',
      '$exp_note',
      now(),
      'expediente'
    );
  " >/dev/null

echo "ok - expediente_app can append outbox events"

expect_fail_sql \
  "expediente_app cannot UPDATE audit_outbox" \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  expediente_app \
  expediente_db \
  "UPDATE audit_outbox
   SET actor_username = 'tampered'
   WHERE event_id = '$exp_event';"

expect_fail_sql \
  "expediente_app cannot DELETE audit_outbox" \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  expediente_app \
  expediente_db \
  "DELETE FROM audit_outbox
   WHERE event_id = '$exp_event';"

expect_fail_sql \
  "expediente_app cannot TRUNCATE audit_outbox" \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  expediente_app \
  expediente_db \
  "TRUNCATE audit_outbox;"

expect_fail_sql \
  "expediente_owner trigger rejects UPDATE audit_outbox" \
  "$EXPEDIENTE_DB_OWNER_PASSWORD" \
  expediente_owner \
  expediente_db \
  "UPDATE audit_outbox
   SET actor_username = 'owner-tampered'
   WHERE event_id = '$exp_event';"

expect_fail_sql \
  "expediente_owner trigger rejects DELETE audit_outbox" \
  "$EXPEDIENTE_DB_OWNER_PASSWORD" \
  expediente_owner \
  expediente_db \
  "DELETE FROM audit_outbox
   WHERE event_id = '$exp_event';"

psql_as \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  expediente_app \
  expediente_db \
  -c "
    INSERT INTO audit_outbox_delivered (event_id)
    VALUES ('$exp_event');
  " >/dev/null

echo "ok - expediente relay can record delivery separately"

echo "== Central audit_log =="

audit_event="$(new_uuid)"
audit_patient="$(new_uuid)"

insert_audit="
  INSERT INTO audit_log (
    event_id,
    clinic_id,
    actor_subject,
    actor_username,
    action,
    patient_id,
    resource_type,
    resource_id,
    occurred_at,
    source_service
  )
  VALUES (
    '$audit_event',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'audit-test-subject',
    'audit-test-user',
    'patient_contact.read',
    '$audit_patient',
    'patient_contact',
    '$audit_patient',
    now(),
    'agenda'
  )
  ON CONFLICT (event_id) DO NOTHING;
"

psql_as \
  "$AUDIT_DB_APP_PASSWORD" \
  audit_app \
  audit_db \
  -c "$insert_audit" >/dev/null

psql_as \
  "$AUDIT_DB_APP_PASSWORD" \
  audit_app \
  audit_db \
  -c "$insert_audit" >/dev/null

count="$(
  psql_as \
    "$AUDIT_DB_APP_PASSWORD" \
    audit_app \
    audit_db \
    -Atc "
      SELECT count(*)
      FROM audit_log
      WHERE event_id = '$audit_event';
    "
)"

if [ "$count" != "1" ]; then
  echo "FAIL: duplicate event produced $count rows"
  exit 1
fi

echo "ok - duplicate delivery produces exactly one audit row"

expect_fail_sql \
  "audit_app cannot UPDATE audit_log" \
  "$AUDIT_DB_APP_PASSWORD" \
  audit_app \
  audit_db \
  "UPDATE audit_log
   SET actor_username = 'tampered'
   WHERE event_id = '$audit_event';"

expect_fail_sql \
  "audit_app cannot DELETE audit_log" \
  "$AUDIT_DB_APP_PASSWORD" \
  audit_app \
  audit_db \
  "DELETE FROM audit_log
   WHERE event_id = '$audit_event';"

expect_fail_sql \
  "audit_app cannot TRUNCATE audit_log" \
  "$AUDIT_DB_APP_PASSWORD" \
  audit_app \
  audit_db \
  "TRUNCATE audit_log;"

expect_fail_sql \
  "audit_owner trigger rejects UPDATE audit_log" \
  "$AUDIT_DB_OWNER_PASSWORD" \
  audit_owner \
  audit_db \
  "UPDATE audit_log
   SET actor_username = 'owner-tampered'
   WHERE event_id = '$audit_event';"

expect_fail_sql \
  "audit_owner trigger rejects DELETE audit_log" \
  "$AUDIT_DB_OWNER_PASSWORD" \
  audit_owner \
  audit_db \
  "DELETE FROM audit_log
   WHERE event_id = '$audit_event';"

echo
echo "All audit idempotency and immutability checks passed."
