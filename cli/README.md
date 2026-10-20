# rostr CLI

The `rostr` command. Behavior is specified in [docs/cli-spec.md](../docs/cli-spec.md); local storage in
[docs/local-storage.md](../docs/local-storage.md), and cloud mode calls the API in [docs/api.md](../docs/api.md).

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
| `ROSTR_REWAKE_DELAY` | How long the draft step waits before waking Claude (default `5s`); tests set `0` |
| `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `CLAUDE_CONFIG_DIR` | Move `~/.config/rostr`, `~/.cache/rostr`, and `~/.claude` |

The end-to-end tests in [`tests/`](../tests/) run this CLI against the real server: `tests/run.sh`.
