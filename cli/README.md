# rostr CLI

The `rostr` command. How it works is in [docs/](../docs/README.md): start with
[architecture.md](../docs/architecture.md) and [hooks.md](../docs/hooks.md).

```sh
go build -o bin/rostr ./cmd/rostr
go test -race ./...
go generate ./...   # after site/src/data/rosters.ts changes (a test fails until you do)
```

## Environment

| Variable | What it does |
|---|---|
| `ROSTR_KEY` | A rostr key. Puts the machine in cloud mode, ahead of `~/.config/rostr/credentials` |
| `ROSTR_API_URL` | The API cloud mode calls. Defaults to `https://api.getrostr.sh`; point it at a local server (`http://localhost:8080` from `docker compose up`) for development and tests |
| `ROSTR_SESSION` | The default for `--session`; the session-start hook writes it to `CLAUDE_ENV_FILE` |
| `ROSTR_AUTO_DRAFT` | `on` or `off`: this machine's auto-draft setting, ahead of `rostr config auto-draft`. Set it in a cloud environment, whose containers start without rostr's local files |
| `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `CLAUDE_CONFIG_DIR` | Move `~/.config/rostr`, `~/.cache/rostr`, and `~/.claude` |

The end-to-end tests in [`tests/`](../tests/) run this CLI against the real server: `tests/run.sh`.
