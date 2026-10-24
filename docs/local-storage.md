# Local storage

Storage for the behavior in [cli-spec.md](cli-spec.md).

In local mode (the default, when there's no rostr key), everything lives in one SQLite database at
`~/.config/rostr/rostr.db`. SQLite is compiled into the CLI (the pure-Go `modernc.org/sqlite`), so there's nothing to
install. The schema mirrors the [server schema](server-schema.md) minus owners and encryption: tasks and repo names
are plaintext here, and `rostr cloud on` encrypts and fingerprints them as it uploads. In cloud mode everything
lives on the API instead; `credentials`, the machine's auto-draft setting, and a cache are kept on disk.

```
~/.config/rostr/
  rostr.db         rosters, project links, drafts, and settings
  credentials      your cloud key (only readable by you), in cloud mode
  auto-draft       "on" or "off": this machine's auto-draft setting, in cloud mode (local mode keeps it in rostr.db);
                   ROSTR_AUTO_DRAFT overrides it
~/.cache/rostr/
  keepalive/<session>       the session's last keep-alive bump (the file's modification time)
  hooks/<agent>             the fingerprint of the hooks rostr last installed for claude-code or codex, so a
                            session start can tell when an upgrade changed them (see cli-spec.md, Upgrading)
  turns/<session>           when the session's last turn ended, and its name and task then, from the Stop hook (or,
                            for a session drafted when it started, the draft); the next prompt or tool call reads it
                            to tell whether the session rested; it's deleted once the next turn's start is recorded
  cloud/sessions/<session>.json   cloud mode: the session's own draft, so later prompts make no call, and whether
                            the agent has been told its name yet (a session drafted when it starts learns it on its
                            first prompt)
```

The session cache is how a session drafted when it starts gets its name on its first prompt without waiting on the
API; see [drafting-latency.md](drafting-latency.md). It holds nothing that isn't on the server: delete it and the next
prompt asks the server.

## Schema

```sql
-- default_roster (a roster name), draft_expiry (e.g. "7d"), and auto_draft ("on" or "off")
create table settings (
  key   text primary key,
  value text not null
);

-- members: [{"name": "Gandalf", "emoji": "🧙"}, ...], in draft order
create table rosters (
  id         integer primary key,
  name       text not null unique,
  emoji_mode text not null default 'on',  -- 'on', 'off', or 'only'
  members    text not null,
  created_at text not null default (datetime('now'))
);

-- Which roster a project uses (`rostr link`), keyed by the normalized "owner/repo"
create table project_links (
  repo      text primary key,
  roster_id integer not null references rosters on delete cascade
);

-- One row per draft, kept for 7 days after release so a resumed session can ask for its old name back
create table drafts (
  id           integer primary key,
  roster_id    integer not null references rosters on delete cascade,
  member_name  text not null,
  member_emoji text not null,              -- kept on the draft so it still shows after the cast is replaced
  copy         integer not null default 1, -- 2 for "Gandalf 2", drafted when every member is busy
  session_id   text not null,
  repo         text,
  task         text,
  drafted_at   text not null default (datetime('now')),
  last_seen_at text not null default (datetime('now')),
  benched_at   text,                        -- null while the agent is active
  turn_started_at text,                    -- when the session's turn last started and ended; resting: ended at
  turn_ended_at   text                     -- least 15 minutes ago, and no later start (added in user_version 3)
);

create unique index drafts_active_member on drafts (roster_id, member_name, copy) where benched_at is null;
create unique index drafts_active_session on drafts (session_id) where benched_at is null;
create index drafts_session on drafts (session_id, drafted_at desc);
```

Names are unique ignoring case and accents; the CLI checks that before writing, since SQLite's `unique` is exact.

## Concurrency

The database runs in WAL mode with a 3-second busy timeout. Every command that writes runs in one
`begin immediate` transaction, which takes SQLite's write lock up front, so two sessions starting at once draft one
after the other instead of racing. Reads never block.

## Drafting

The same steps as the server, in one transaction:

1. **Expire stale drafts:** release active drafts whose `last_seen_at` is older than `draft_expiry`, and delete
   released rows older than 7 days.
2. **Same session, same name:** if the session has an active draft (in any roster), bump `last_seen_at` and return it.
3. **Resume:** if the session had a member before and that member is free, draft them again, from that roster.
4. **Otherwise** draft the free member drafted least recently (never drafted first, then roster order). If
   every member is busy, draft a numbered copy (`copy` 2, then 3) by the same rule.

## Editing

`rostr edit` opens the roster in the cast editor, then a page for its emoji mode, and saves the emoji mode and
members together when Save is pressed. Saving refuses to remove or rename a drafted member.

## Moving to the cloud

`rostr cloud on` uploads every roster, the default roster, project links, and active drafts in one request,
creates your key, and only then deletes `rostr.db`. There's no way back to local in v1. A machine is in cloud mode
whenever it has a key, from `credentials` or the `ROSTR_KEY` environment variable.
