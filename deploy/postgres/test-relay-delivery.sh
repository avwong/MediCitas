#!/bin/sh
set -eu

HOST="${PGHOST:-postgres}"

: "${AGENDA_DB_APP_PASSWORD:?required}"
: "${EXPEDIENTE_DB_APP_PASSWORD:?required}"
: "${AUDIT_DB_APP_PASSWORD:?required}"

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

wait_for_one() {
  label="$1"
  password="$2"
  user="$3"
  database="$4"
  sql="$5"

  attempt=0

  while [ "$attempt" -lt 30 ]; do
    value="$(
      psql_as \
        "$password" \
        "$user" \
        "$database" \
        -Atc "$sql" \
        2>/dev/null || true
    )"

    if [ "$value" = "1" ]; then
      echo "PASS: $label"
      return 0
    fi

    attempt=$((attempt + 1))
    sleep 1
  done

  echo "FAIL: $label"
  exit 1
}

test_clinic="eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

echo "== Agenda -> Audit relay =="

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
      '$test_clinic',
      'relay-test-agenda-subject',
      'relay-test-agenda',
      'patient_contact.read',
      '$agenda_patient',
      'patient_contact',
      '$agenda_patient',
      now(),
      'agenda'
    );
  " >/dev/null

wait_for_one \
  "Agenda relay marks event delivered" \
  "$AGENDA_DB_APP_PASSWORD" \
  agenda_app \
  agenda_db \
  "SELECT count(*)
   FROM audit_outbox_delivered
   WHERE event_id = '$agenda_event';"

wait_for_one \
  "Audit receives Agenda event exactly once" \
  "$AUDIT_DB_APP_PASSWORD" \
  audit_app \
  audit_db \
  "SELECT count(*)
   FROM audit_log
   WHERE event_id = '$agenda_event'
     AND clinic_id = '$test_clinic'
     AND action = 'patient_contact.read'
     AND source_service = 'agenda';"

echo ""
echo "== Expediente -> Audit relay =="

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
      '$test_clinic',
      'relay-test-expediente-subject',
      'relay-test-expediente',
      'clinical_note.read',
      '$exp_patient',
      'clinical_note',
      '$exp_note',
      now(),
      'expediente'
    );
  " >/dev/null

wait_for_one \
  "Expediente relay marks event delivered" \
  "$EXPEDIENTE_DB_APP_PASSWORD" \
  expediente_app \
  expediente_db \
  "SELECT count(*)
   FROM audit_outbox_delivered
   WHERE event_id = '$exp_event';"

wait_for_one \
  "Audit receives Expediente event exactly once" \
  "$AUDIT_DB_APP_PASSWORD" \
  audit_app \
  audit_db \
  "SELECT count(*)
   FROM audit_log
   WHERE event_id = '$exp_event'
     AND clinic_id = '$test_clinic'
     AND action = 'clinical_note.read'
     AND source_service = 'expediente';"

echo ""
echo "All audit relay delivery checks passed."
