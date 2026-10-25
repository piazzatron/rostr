# Storage

Both backends store the same four things: settings, rosters, project links, and drafts. The schemas are in the code:
the `schema` constant in `cli/internal/store/sqlite.go` (local) and the migrations in
`server/internal/api/migrations/` (server, embedded and run at startup).

## Files on disk

```
~/.config/rostr/                 ($XDG_CONFIG_HOME)
  rostr.db                       local mode: everything
  credentials                    cloud mode: the rostr key (mode 0600)
  auto-draft                     cloud mode: this machine's auto-draft setting; ROSTR_AUTO_DRAFT overrides it
~/.cache/rostr/                  ($XDG_CACHE_HOME)
  keepalive/<session>            the last keep-alive bump, as the file's mtime
  turns/<session>                the last turn's end, with the session's name and task, until the next start is reported
  cloud/sessions/<session>.json  cloud mode: the session's own draft, so later prompts make no call
  hooks/<agent>                  the fingerprint of the last hook install (see hooks.md, Upgrading)
  presets.json                   the pre-built rosters last fetched from getrostr.sh (see architecture.md)
  background.log                 errors from background hook processes
```

Everything under `~/.cache/rostr` can be deleted: it holds nothing that isn't in the store.

## Data model

- **rosters:** a name, an emoji mode (`on`, `off`, `only`), and the members as one JSON array in draft order. Members
  are always read and written whole (`rostr edit` opens the entire roster). Names are unique ignoring case and accents.
- **project_links:** which roster a repo uses.
- **drafts:** one row per draft: roster, member name and emoji (kept so it still shows after a cast is replaced), copy
  number, session ID, repo, task, and the times `drafted_at`, `last_seen_at`, `benched_at` (null while active),
  `turn_started_at`, `turn_ended_at`. Rows are kept 7 days after benching so a resumed session can ask for its old
  name back. Partial unique indexes on active drafts allow one draft per name and copy, and one per session.
- **settings:** the default roster, draft expiry, and (local only) auto-draft.

## Local: SQLite

Pure-Go `modernc.org/sqlite`, compiled into the CLI. WAL mode, a 3-second busy timeout, and immediate transactions:
writes (and reads that expire stale drafts first) take the write lock up front, so concurrent hooks queue rather than
race. Tasks and repo names are plaintext.

## Server: Postgres

The same shapes, scoped to an owner (one per rostr key). `owners.id` is the hex SHA-256 of the auth token, the only
trace of it the server keeps. Tasks and repo names arrive encrypted and are stored as ciphertext; project links are
keyed by an HMAC fingerprint of the repo (see [cloud.md](cloud.md)). Disbanding an owner's last roster deletes the owner
and everything under it.
