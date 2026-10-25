# Architecture

rostr gives every coding-agent session (Claude Code, Codex) a name drafted from a roster, like 🧙 Gandalf from The
Fellowship. The name goes at the front of the session title (`🧙 Gandalf · Fix auth redirect loop`) and is freed when
the session ends, rests and is needed elsewhere, or goes unseen past its expiry.

## Pieces

```
Claude Code / Codex ──hook JSON──▶ rostr hook … ──▶ store ──┬─▶ SQLite (~/.config/rostr/rostr.db)     local mode
                                   (cli/)                   └─▶ api.getrostr.sh (server/, Postgres)    cloud mode
                                                                     │
                                                                     └─▶ live.getrostr.sh (live/) ──▶ getrostr.sh (site/)
```

| Folder | What it is |
|---|---|
| `cli/` | The `rostr` binary (Go, macOS and Linux): every command, the hooks, the installers for Claude Code and Codex |
| `server/` | The API behind cloud mode (Go, `net/http`, `pgx`, Postgres), hosted on Railway |
| `live/` | A Cloudflare Worker with one Durable Object that broadcasts the home page's live counts |
| `site/` | getrostr.sh (Astro). `site/src/data/rosters.ts` is the source for the preset rosters; `go generate` copies them into the CLI |
| `tests/` | End-to-end tests: the real CLI against the real server and Postgres (`tests/run.sh`) |

Inside the CLI:

| Package | Role |
|---|---|
| `internal/app` | Every command and hook handler (`hook.go`, `hook_codex.go`), the setup wizard and cast editor |
| `internal/store` | The storage interface and its two backends: `sqlite.go` (local) and `cloud.go` (the API, plus a per-session cache) |
| `internal/claudecode`, `internal/codex` | Each agent's hook input/output shapes, installers, transcript checks, and the text the agent is told |
| `internal/keepalive` | Per-session files the hooks keep between prompts (the hourly keep-alive stamp, the turn-end record) |
| `internal/roster` | Domain rules shared by both backends: members, loose name matching, display |
| `internal/crypto` | The rostr key and the auth token, encryption key, and fingerprint key derived from it |
| `internal/project`, `internal/pane`, `internal/paths`, `internal/presets` | Repo detection, tmux/herdr labels, file locations, generated presets |

## Local or cloud

A whole setup is one or the other, never mixed.

- **Local** (the default): one SQLite database, compiled into the CLI. Its only network call fetches the pre-built
  rosters (below).
- **Cloud:** everything lives on the API, so sessions on any machine or in cloud environments (Claude Code on the
  web, Codex cloud) draft from the same rosters. A machine is in cloud mode whenever it has a rostr key, from
  `~/.config/rostr/credentials` or `ROSTR_KEY`. `rostr cloud on` uploads a local setup in one request and deletes the
  local database only once that succeeds. There's no way back. Joining a setup with a key installs the hooks, as
  `rostr assemble` does on a first machine.

Both backends implement `store.Store`, so commands and hooks don't know which one they're on, and follow the same
drafting rules (see [drafting.md](drafting.md)).

## Sessions

Every draft belongs to a session ID from the agent. Hooks pass it with `--session`; commands the agent runs find it in
`ROSTR_SESSION` (written by the Claude Code start hook to `CLAUDE_ENV_FILE`), `CODEX_THREAD_ID` (set by Codex), or
`CLAUDE_CODE_SESSION_ID` (set by Claude Code, so the session that sets rostr up, which started before the hooks were
installed, can draft itself).
Drafting only happens inside a session.

Everything for a session finds its draft by session ID, so an agent that `cd`s into another project keeps its name.
Only a session that has lost its name and can't get it back drafts from the roster of the project it's in now.

## Projects and which roster is drafted

A project is its git `origin` as `owner/repo`, lowercased (the last two path segments, `.git` dropped), so it's the
same on every clone, worktree, and machine. Without an `origin` it's the repo's main folder name; outside git there's
no project. A draft uses the `--roster` flag, else the project's linked roster (`rostr link`), else the default roster.

## Failure model

Hooks never block a session: `rostr hook` always exits 0, network calls time out after 3 seconds, and anything slow
or optional (summaries, turn reports, hook upgrades, drafting ahead) runs in a detached background process that logs
to `~/.cache/rostr/background.log`. If rostr can't draft, the session just starts unnamed.

## Pre-built rosters

`site/src/data/rosters.ts` is the one list. The site publishes it at `getrostr.sh/rosters.json`
(`site/src/pages/rosters.json.ts`), and `go generate` copies it into the CLI (`internal/presets`). `rostr presets` and
`rostr assemble` fetch the published list when their cached copy (`~/.cache/rostr/presets.json`) is missing or a day
old, with a 3-second timeout, and otherwise use the cache; with neither, the built-in list. So a new roster reaches
every CLI within a day of the site deploying, without a release. Hooks never fetch it. A list with a `version` newer
than the CLI knows is ignored, and so are presets in it that don't make a valid roster of at least 16.
