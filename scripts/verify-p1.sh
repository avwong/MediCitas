#!/bin/sh
# Verificación completa: pruebas unitarias, análisis estático y todas las
# pruebas de integración contra la pila real de Compose. No borra volúmenes.
set -eu

export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-1}"

run() {
  name="$1"
  shift

  echo
  echo "=== $name ==="
  "$@"
}

run "Go tests" \
  go test ./...

run "Go vet" \
  go vet ./...

run "Compose validation" \
  docker compose config --quiet

run "Start complete stack" \
  docker compose up -d --build

run "P1 API integration" \
  docker compose --profile test run --build --rm p1-api-integration-test

run "Agenda API integration" \
  docker compose --profile test run --build --rm agenda-api-integration-test

run "Appointments API integration and booking race" \
  docker compose --profile test run --build --rm citas-api-integration-test

run "Tenant isolation leaks" \
  docker compose --profile test run --build --rm leak-integration-test

run "Database privilege boundaries" \
  docker compose --profile test run --rm db-security-test

run "Audit immutability and idempotency" \
  docker compose --profile test run --rm audit-invariants-test

run "Audit relay delivery" \
  docker compose --profile test run --rm audit-relay-test

echo
echo "All P1 verification checks passed."
