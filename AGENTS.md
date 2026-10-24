# rostr

rostr gives each coding-agent session a name drafted from a roster. Go CLI in `cli/`, Go API server on Postgres in
`server/`, end-to-end tests in `tests/`, the Astro site in `site/`, and the live-counts Worker in `live/`.
[CONTRIBUTING.md](CONTRIBUTING.md) has the layout and how to build each part; [docs/](docs/) has the design docs, with
[docs/cli-spec.md](docs/cli-spec.md) the source of truth for how the CLI behaves.

## Checks

Run before you commit:

```sh
make fmt     # fix formatting and imports
make check   # golangci-lint and unit tests for every Go module; starts the compose Postgres for the server tests
```

`make e2e` runs the end-to-end tests against the full Docker Compose stack. CI runs all of it, plus `make vuln`.

## Conventions

- When CLI behavior changes, update `docs/cli-spec.md` too, and `site/src/pages/cli.astro` if a command or flag
  changes. API changes go in `docs/api.md`, and schema changes in a new migration under
  `server/internal/api/migrations/` and in `docs/server-schema.md`.
- Server logs use `log/slog`. Inside a handler, add to the request's log line with `logAttrs` rather than logging on
  your own (see `server/internal/api/logging.go`). Never log auth tokens, session IDs, or the ciphertext.
- Tests check behavior, not wording. Don't assert on the text of messages, errors, help, or other output meant for people,
  whether by exact match or substring: check exit codes, `--json` output, and state (`stats --json`, `config --json`,
  files on disk, the database) instead. Data in the output, like a member's name, is fine to look for. Output another
  program reads is a contract and is fine to check exactly: hook JSON, session titles, and what rostr writes to
  settings files or passes to tmux, herdr, or Codex. Driving a terminal UI may wait on a prompt's text, but check the
  result through state.
- Pull request titles start with `feat:`, `fix:`, `improve:`, `refactor:`, or `chore:`.
