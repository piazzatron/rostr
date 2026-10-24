# docs

High-level notes on how rostr's systems fit together. The code is the source of truth: these docs explain the
shape of each system and point to where it lives, and leave exact behavior (flags, request bodies, column lists,
output text) to the code and its tests. When a change alters how a system works, update the doc that describes it.

| Doc | What it covers |
|---|---|
| [architecture.md](architecture.md) | The pieces (CLI, hooks, API server, live Worker, site), local vs cloud mode, sessions, projects, and where the code lives |
| [hooks.md](hooks.md) | How rostr plugs into Claude Code and Codex: hook events, the first-prompt draft step, session titles, identity, compaction, tmux and herdr, upgrades |
| [drafting.md](drafting.md) | How a session gets a name: the pick, resuming, copies, concurrency, and drafting ahead in cloud mode |
| [keepalive.md](keepalive.md) | How a session keeps its name: turns, resting, the hourly keep-alive, expiry, and how to test them end to end |
| [storage.md](storage.md) | The local SQLite database and cache files, and the server's Postgres schema |
| [cloud.md](cloud.md) | The rostr key and what's derived from it, and the HTTP API behind cloud mode |
| [live-counts.md](live-counts.md) | The home page's live agent counts: the server push, the Worker, and the socket |
