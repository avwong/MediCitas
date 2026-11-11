#!/bin/sh
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

run "Database privilege boundaries" \
  docker compose --profile test run --rm db-security-test

run "Audit immutability and idempotency" \
  docker compose --profile test run --rm audit-invariants-test

run "Audit relay delivery" \
  docker compose --profile test run --rm audit-relay-test

echo
echo "All P1 verification checks passed."
