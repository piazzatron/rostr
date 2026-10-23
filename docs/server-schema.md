# Server schema

Storage for the behavior in [cli-spec.md](cli-spec.md). The endpoints are in [api.md](api.md).

The rostr API (`api.getrostr.sh`) stores cloud rosters in Postgres. Local rosters never touch it.

Everything an owner has hangs off their key. There are no accounts: the key is the identity.

The server never sees the rostr key itself. The CLI derives three keys from it with HKDF: an auth token (sent to the
server), a task encryption key, and a repo fingerprint key. Only the auth token ever leaves the user's machine. See
the site's /privacy page.

```sql
-- One row per rostr key. id is the hex SHA-256 of the derived auth token, the only trace of the token the server keeps,
-- so a request knows its owner without a lookup. Deleted (with everything under it) when the owner disbands their last
-- roster. Auto-draft isn't here: it's a setting of each machine.
create table owners (
  id                text primary key,
  default_roster_id uuid,  -- set after the first roster is created
  draft_expiry      interval not null default '7 days',  -- `rostr config expiry`
  created_at        timestamptz not null default now()
);

-- A roster and all its members. Members are always read and written as a whole (`rostr edit` opens the entire
-- roster), so they live in JSON columns. Array order is draft order, A team first.
-- a_team, b_team: [{"name": "Gandalf", "emoji": "🧙"}, ...]
create table rosters (
  id            uuid primary key default gen_random_uuid(),
  owner_id      text not null references owners on delete cascade,
  name          text not null,
  name_key      text not null,  -- name lowercased with accents stripped (by the API), so names are unique loosely
  emoji_mode    text not null default 'on' check (emoji_mode in ('on', 'off', 'only')),
  a_team        jsonb not null,
  b_team        jsonb not null default '[]',
  created_at    timestamptz not null default now(),
  constraint rosters_owner_name unique (owner_id, name_key)
);

alter table owners
  add foreign key (default_roster_id) references rosters on delete set null;
create index owners_default_roster on owners (default_roster_id);  -- for the set null when a roster is disbanded

-- Which roster a project uses (`rostr link`). Projects without a link use the owner's default roster.
-- The server never sees repo names. The CLI sends two values, both computed with keys that never leave the user's
-- machine: repo_hash, HMAC-SHA256 of the lowercased "owner/repo" with the fingerprint key, for lookups; and repo,
-- AES-256-GCM ciphertext of "owner/repo", so any machine with the key can show and restore the name.
create table project_links (
  owner_id  text not null references owners on delete cascade,
  repo_hash text not null,
  repo      bytea not null,
  roster_id uuid not null references rosters on delete cascade,
  primary key (owner_id, repo_hash)
);

create index project_links_roster on project_links (roster_id);  -- for the cascade when a roster is disbanded

-- One row per draft. Rows are kept for 7 days after release so a resumed session can ask for its old name back.
create table drafts (
  id           bigint generated always as identity primary key,
  owner_id     text not null references owners on delete cascade,  -- the roster's owner, for the session index
  roster_id    uuid not null references rosters on delete cascade,
  member_name  text not null,
  member_emoji text not null,  -- kept on the draft so it still shows after the cast is replaced
  copy         int not null default 1,  -- 2 for "Gandalf 2", drafted when every member is busy
  session_id   text not null,
  repo_hash    text,          -- same keyed hash as project_links
  repo         bytea,         -- same ciphertext as project_links
  task         bytea,         -- AES-256-GCM ciphertext, encrypted on the user's machine; the server can't read it
  drafted_at   timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),  -- bumped by re-drafts, `rostr task`, turns, and the keep-alive
  benched_at   timestamptz,   -- null while the agent is active
  -- When the session's turn last started and ended, as the client reported them (POST /v1/agents/turn). Resting:
  -- turn_ended_at at least 15 minutes ago, and no later turn_started_at
  turn_started_at timestamptz,
  turn_ended_at   timestamptz
);

-- A member can only be drafted once at a time, and a session can only hold one member at a time, across all of
-- the owner's rosters.
create unique index drafts_active_member on drafts (roster_id, member_name, copy) where benched_at is null;
create unique index drafts_active_session on drafts (owner_id, session_id) where benched_at is null;

-- A session's drafts, newest first (resuming), which also serves the cascade when an owner is deleted.
create index drafts_owner_session on drafts (owner_id, session_id, drafted_at desc);

-- The cascade when a roster is disbanded, and the background sweep's two passes.
create index drafts_roster on drafts (roster_id);
create index drafts_active_last_seen on drafts (last_seen_at) where benched_at is null;
create index drafts_benched on drafts (benched_at) where benched_at is not null;
```

## Drafting

A draft makes two round trips to the database (see [api.md](api.md#post-v1agentsdraft)):

1. **One batch of reads:** it bumps the session's active draft if it has one (which is all a draft of a drafted
   session does), and reads the owner's default roster, their rosters, the project's link, the session's previous
   draft, the owner's active drafts (with which are resting), and when each member was last drafted
   (`max(drafted_at)` per member over the retained drafts).
2. **The pick, in Go:** resume the session's previous member if they're free; else the free member drafted least
   recently (never drafted first, then roster order), A team before the B team; if every member is busy, the name of
   the member who has rested longest; else a free numbered copy (`copy` 2, then 3).
3. **One batch of writes,** run as one implicit transaction: bench the resting draft whose name it takes, if any
   (only if it's still resting), and insert the new draft.

There's no database lock. The unique indexes are what keep two drafts from taking the same name or session: when two
race, one insert fails, and that draft starts over and sees the other's. That also covers a resting session that
woke meanwhile: its draft isn't benched, so the insert of its name fails. Drafts for one owner on one server instance
take turns, so a burst of sessions starting at once doesn't keep colliding; the indexes catch races between instances.

Every other write (edits, links, settings, benches) runs in a transaction holding the owner's row lock.

## Expiry

A background sweep in each server instance runs every minute: it benches drafts whose `last_seen_at` is older than
their owner's `draft_expiry` (the backstop for sessions that die without running the release hook), and deletes drafts
benched more than 7 days ago, past the window a resumed session can ask for its old name back. Running on several
instances at once is harmless. Until the sweep benches an expired draft, it still counts as active.

## Releasing

`rostr bench` (and the session-end hook) sets `benched_at = now()` on the active draft.

## Not in the schema

- Rate-limiting key creation by IP happens in the API, not in a table.
- Local mode uses a SQLite database with the same shapes; see [local-storage.md](local-storage.md).
