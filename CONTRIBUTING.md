# Contributing to rostr

Thanks for helping out. Bug reports, fixes, and new rosters are all welcome.

## Submitting a roster

The easiest contribution: [open a roster submission](https://github.com/piazzatron/rostr/issues/new?template=roster-submission.yml)
with the roster's name, where it's from, and its members, each with an emoji, at least 16, the best-known first: a team of n takes
the first n.

To add one yourself, edit [`site/src/data/rosters.ts`](site/src/data/rosters.ts), then regenerate the CLI's copy:

```sh
cd cli && go generate ./...
```

CI fails if the two are out of sync.

## Repo layout

| Folder | What's in it |
|---|---|
| `cli/` | The `rostr` CLI and its agent hooks (Go). See [`cli/README.md`](cli/README.md) |
| `server/` | The API at api.getrostr.sh (Go, Postgres). See [`server/README.md`](server/README.md) |
| `site/` | getrostr.sh (Astro) |
| `tests/` | End-to-end tests: the real CLI against a real server |
| `docs/` | Design docs. [`docs/cli-spec.md`](docs/cli-spec.md) is the source of truth for how the CLI behaves |

## Building and testing

You need Go (the version in `cli/go.mod`), Docker for the server and end-to-end tests, and Node 22.22+ for the site.

**CLI**

```sh
cd cli
go build -o bin/rostr ./cmd/rostr
go vet ./... && gofmt -l .
go test -race ./...
```

**Server** (tests run against real Postgres)

```sh
docker compose up -d --wait postgres
cd server
TEST_DATABASE_URL='postgres://rostr:rostr@localhost:5432/rostr?sslmode=disable' go test -race ./...
```

**End-to-end** (starts Postgres and the server in Docker, runs the real CLI against them, then tears it all down)

```sh
tests/run.sh
```

In a Claude Code cloud environment, set the environment's setup script to `bash rostr/scripts/cloud-setup.sh`. It
installs what the tests need (Codex included), starts Docker, and builds the images `tests/run.sh` uses, so all of the
above works there too.

**Site**

```sh
cd site
npm install
npm run dev     # http://localhost:4321
npm run build
```

To try the CLI against your local server, start it with `docker compose up` and set
`ROSTR_API_URL=http://localhost:8080`.

## Making a change

1. For anything bigger than a small fix, open an issue first so we can agree on the approach.
2. If the change affects how the CLI behaves, update [`docs/cli-spec.md`](docs/cli-spec.md) along with the code, and
   the CLI docs at `site/src/pages/cli.astro` if a command or flag changes.
3. Add or update tests, and make sure `go vet`, `gofmt`, and the tests pass. CI runs the CLI, server, and end-to-end
   suites on every pull request.
4. Open a pull request with a short title starting with `feat:`, `fix:`, `improve:`, `refactor:`, or `chore:`, and say
   how you verified it.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
