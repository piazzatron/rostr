#!/usr/bin/env bash
# Runs the end-to-end tests: starts Postgres and the API server with Docker Compose (as their own compose project, on
# their own ports, so a dev stack is left alone), runs the tests in this folder against them with the real CLI, and
# tears everything down, volumes included. Extra arguments go to `go test`, e.g. tests/run.sh -run TestFirstSetup.
set -euo pipefail
cd "$(dirname "$0")/.."

export COMPOSE_PROJECT_NAME=rostr-e2e
export POSTGRES_PORT="${POSTGRES_PORT:-55432}"
export SERVER_PORT="${SERVER_PORT:-58080}"

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    docker compose logs server || true
  fi
  docker compose down -v --remove-orphans
  exit "$status"
}
trap cleanup EXIT

docker compose up -d --build --wait

cd tests
ROSTR_E2E_API_URL="http://localhost:$SERVER_PORT" \
ROSTR_E2E_DATABASE_URL="postgres://rostr:rostr@localhost:$POSTGRES_PORT/rostr?sslmode=disable" \
  go test -race -count=1 "$@" ./...
