#!/usr/bin/env bash
# Runs the end-to-end tests without Docker, for environments like Claude Code cloud sessions that have Postgres but no
# Docker daemon: rebuilds the rostr_e2e database on the local Postgres, starts the API server from this checkout on a
# free port, runs the tests against it, and stops the server. Extra arguments go to `go test`, as with run.sh.
# ROSTR_KEY is cleared, so the tests never touch a real cloud setup.
set -euo pipefail
cd "$(dirname "$0")/.."

database=${ROSTR_E2E_DATABASE:-rostr_e2e}
admin_url=${ROSTR_E2E_ADMIN_URL:-postgres://rostr:rostr@localhost:5432/rostr?sslmode=disable}
database_url="postgres://rostr:rostr@localhost:5432/$database?sslmode=disable"
port=$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')

psql -q "$admin_url" -c "drop database if exists $database" -c "create database $database"

server=$(mktemp -d)/rostr-server
(cd server && go build -o "$server" .)
DATABASE_URL=$database_url PORT=$port RATE_LIMIT_REGISTRATIONS_PER_HOUR=10000 RATE_LIMIT_REQUESTS_PER_MINUTE=100000 \
  "$server" > "$(dirname "$server")/server.log" 2>&1 &
server_pid=$!
cleanup() {
  status=$?
  kill "$server_pid" 2>/dev/null || true
  if [ "$status" -ne 0 ]; then
    cat "$(dirname "$server")/server.log" >&2
  fi
  exit "$status"
}
trap cleanup EXIT

for _ in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$port/health" >/dev/null && break
  sleep 0.2
done

cd tests
env -u ROSTR_KEY ROSTR_E2E_API_URL="http://127.0.0.1:$port" ROSTR_E2E_DATABASE_URL="$database_url" \
  go test -race -count=1 "$@" ./...
