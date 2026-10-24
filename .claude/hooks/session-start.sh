#!/bin/bash
# Sets up a Claude Code cloud session so every test suite runs: Postgres for the server and end-to-end tests, the Go
# modules, the site's packages, and Codex for checking rostr's Codex hooks. Safe to run again; the container is cached
# after it finishes.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

# The Codex version the hooks were last checked against (docs/integrations-research.md)
CODEX_VERSION=0.164.0

cd "$CLAUDE_PROJECT_DIR"

# Postgres, with the rostr role and the databases the tests use (there's no Docker here for tests/run.sh)
if command -v pg_ctlcluster >/dev/null; then
  cluster=$(pg_lsclusters --no-header | awk 'NR == 1 {print $1, $2}')
  # shellcheck disable=SC2086
  pg_ctlcluster $cluster start >/dev/null 2>&1 || true
  for _ in $(seq 1 30); do
    pg_isready -q -h localhost && break
    sleep 1
  done
  psql_as_postgres() { su postgres -c "psql -qtA -c \"$1\""; }
  if [ -z "$(psql_as_postgres "select 1 from pg_roles where rolname = 'rostr'")" ]; then
    psql_as_postgres "create role rostr login superuser password 'rostr'"
  fi
  for db in rostr rostr_e2e; do
    if [ -z "$(psql_as_postgres "select 1 from pg_database where datname = '$db'")" ]; then
      psql_as_postgres "create database $db owner rostr"
    fi
  done
  echo "export TEST_DATABASE_URL='postgres://rostr:rostr@localhost:5432/rostr?sslmode=disable'" >> "$CLAUDE_ENV_FILE"
else
  echo "session-start: Postgres isn't installed, so the server and end-to-end tests can't run" >&2
fi

# Go modules for the CLI, the server, and the end-to-end tests
for module in cli server tests; do
  (cd "$module" && go mod download)
done

# The site's packages, for `npm run build`. npm ci, since an older npm's `npm install` rewrites the lockfile
(cd site && npm ci --no-audit --no-fund --loglevel=error)

# Codex, to run rostr's Codex hooks for real
if [ "$(codex --version 2>/dev/null)" != "codex-cli $CODEX_VERSION" ]; then
  npm install -g --no-audit --no-fund --loglevel=error "@openai/codex@$CODEX_VERSION"
fi
