# rostr API server

The API at `api.getrostr.sh`: Go (`net/http`, `pgx`) on Postgres. The contract is [docs/api.md](../docs/api.md) and the
schema is [docs/server-schema.md](../docs/server-schema.md). Migrations in `internal/api/migrations/` are embedded and
run on startup.

Environment:

- `DATABASE_URL` (required): Postgres connection string.
- `PORT` (default `8080`).
- `RATE_LIMIT_REGISTRATIONS_PER_HOUR` (default `20`, per IP) and `RATE_LIMIT_REQUESTS_PER_MINUTE` (default `300`,
  per auth token).
- `LIVE_URL` and `LIVE_SECRET` (optional): the live Worker's URL and shared secret. With both set, the server pushes
  fresh home-page counts to it after writes; see [docs/live-counts.md](../docs/live-counts.md).
- `LOG_LEVEL` (default `info`): `debug`, `info`, `warn`, or `error`.

## Logs

The server logs JSON to stderr, one object per line. Every request gets one `request` line with its `route` (the
matched pattern, like `PUT /v1/rosters/{name}`), `status`, `ms`, and `owner` (the first 8 characters of the owner ID),
plus what it did: the `roster` it created, edited, or disbanded, or the `roster`, `member`, and `copy` it drafted or
benched. 4xx responses log at warn and 5xx at error, with the `error`. Health checks log only when they fail. The
draft sweep logs how many drafts it benched and deleted. Auth tokens, session IDs, and the encrypted task and repo are
never logged.

## Local development

From the repo root, with Docker:

```sh
docker compose up --build          # Postgres and the server, on localhost:5432 and localhost:8080
docker compose up -d --build --wait # the same, in the background, returning once both are healthy
curl localhost:8080/health
docker compose down                # stop (add -v to also delete the database volume)
```

Set `POSTGRES_PORT` or `SERVER_PORT` if 5432 or 8080 is taken. The compose server's rate limits are raised so
end-to-end tests can register many owners.

## Tests

The tests run against real Postgres: start the compose database, then run them from `server/`.

```sh
docker compose up -d --wait postgres
cd server
TEST_DATABASE_URL='postgres://rostr:rostr@localhost:5432/rostr?sslmode=disable' go test -race ./...
```

Each test registers its own owners, so the tests run in parallel and can share the database with a running compose
server.

## CLI end-to-end tests

`tests/run.sh` (from the repo root) starts this stack as its own compose project on ports 55432 and 58080, runs the
real CLI against it, and tears it down. See [`tests/`](../tests/).
