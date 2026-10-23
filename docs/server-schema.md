# Server schema

Storage for the behavior in [cli-spec.md](cli-spec.md). The endpoints are in [api.md](api.md).

The rostr API (`api.getrostr.sh`) stores cloud rosters in Postgres. Local rosters never touch it.

Everything an owner has hangs off their key. There are no accounts: the key is the identity.

The server never sees the rostr key itself. The CLI derives three keys from it with HKDF: an auth token (sent to the
server), a task encryption key, and a repo fingerprint key. Only the auth token ever leaves the user's machine. See
the site's /privacy page.

```sql
-- One row per rostr key. Only a SHA-256 hash of the derived auth token is stored. Deleted (with everything under it)
-- when the owner disbands their last roster.
create table owners (
  id                uuid primary key default gen_random_uuid(),
  auth_token_hash   text not null unique,
  default_roster_id uuid,  -- set after the first roster is created
  draft_expiry      interval not null default '7 days',  -- `rostr config expiry`
  auto_draft        boolean not null default true,       -- `rostr config auto-draft`
  created_at        timestamptz not null default now()
);

-- A roster and all its members. Members are always read and written as a whole (`rostr edit` opens the entire
-- roster), so they live in JSON columns. Array order is draft order, A team first.
-- a_team, b_team: [{"name": "Gandalf", "emoji": "🧙"}, ...]
create table rosters (
  id            uuid primary key default gen_random_uuid(),
  owner_id      uuid not null references owners on delete cascade,
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

-- Which roster a project uses (`rostr link`). Projects without a link use the owner's default roster.
-- The server never sees repo names. The CLI sends two values, both computed with keys that never leave the user's
-- machine: repo_hash, HMAC-SHA256 of the lowercased "owner/repo" with the fingerprint key, for lookups; and repo,
-- AES-256-GCM ciphertext of "owner/repo", so any machine with the key can show and restore the name.
create table project_links (
  owner_id  uuid not null references owners on delete cascade,
  repo_hash text not null,
  repo      bytea not null,
  roster_id uuid not null references rosters on delete cascade,
  primary key (owner_id, repo_hash)
);

create index project_links_roster on project_links (roster_id);  -- for the cascade when a roster is disbanded

-- One row per draft. Rows are kept for 7 days after release so a resumed session can ask for its old name back.
create table drafts (
  id           bigint generated always as identity primary key,
  owner_id     uuid not null references owners on delete cascade,  -- the roster's owner, for the session index
  roster_id    uuid not null references rosters on delete cascade,
  member_name  text not null,
  member_emoji text not null,  -- kept on the draft so it still shows after the cast is replaced
  copy         int not null default 1,  -- 2 for "Gandalf 2", drafted when every member is busy
  session_id   text not null,
  repo_hash    text,          -- same keyed hash as project_links
  repo         bytea,         -- same ciphertext as project_links
  task         bytea,         -- AES-256-GCM ciphertext, encrypted on the user's machine; the server can't read it
  drafted_at   timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),  -- bumped by re-drafts, `rostr task`, and the hourly keep-alive
  released_at  timestamptz    -- null while the agent is active
);

-- A machine's next pick in a roster, held so no other machine drafts that member first (docs/api.md, Reservations).
-- Made and extended by every response carrying the next state, for 24 hours; deleted when its member is drafted.
create table reservations (
  roster_id   uuid not null references rosters on delete cascade,
  machine_id  text not null,
  member_name text not null,  -- the cast's spelling
  expires_at  timestamptz not null,
  primary key (roster_id, machine_id)
);

-- A member can only be drafted once at a time, and a session can only hold one member at a time, across all of
-- the owner's rosters.
create unique index drafts_active_member on drafts (roster_id, member_name, copy) where released_at is null;
create unique index drafts_active_session on drafts (owner_id, session_id) where released_at is null;

-- For resuming: find the member a session had before.
create index drafts_session on drafts (session_id, drafted_at desc);

-- For the cascade when a roster is disbanded, and pruning released drafts.
create index drafts_roster on drafts (roster_id, released_at);
```

## Drafting

`rostr draft` runs in one transaction:

1. **Lock the owner row** (`select ... from owners where id = $1 for update`). This serializes an owner's drafts, so
   two sessions starting at once can't both pick Gandalf. It's the owner rather than the roster because a draft can
   touch more than one roster: "same session" looks across all of them, and a resume drafts into the session's old
   roster, which may not be the one asked for. Contention is still one person's own sessions, so the lock is never
   busy for long. Every other write (edits, links, releases, settings) takes the same lock.
2. **Expire stale drafts:** release any of the owner's active drafts whose `last_seen_at` is older than their
   `draft_expiry`. This is the backstop for sessions that die without running the release hook. Also delete released
   rows older than 7 days, and expired reservations. It runs here, lazily, at the start of every write, so there's
   no cleanup job. Reads leave out drafts past the expiry that haven't been released yet.
3. **Same session, same name:** if the session already has an active draft in any of the owner's rosters (found by
   `session_id`), bump `last_seen_at` and return it.
4. **Resume:** if the session had a member before and that member is free, draft them again, from that roster.
5. **Otherwise** draft the free member drafted least recently (`max(drafted_at)` per member over the retained
   drafts; never drafted first, then roster order), A team before the B team, skipping members other machines hold
   reservations on, or the `preferred` member the CLI already
   announced (their lowest free copy if their name is taken), leaving out the members in `avoid`. If every member
   is busy, draft a free numbered copy (`copy` 2, then 3). See [api.md](api.md#post-v1agentsdraft).

The unique indexes back this up: if the logic ever gets it wrong, the insert fails instead of drafting a duplicate.

## Releasing

`rostr release` (and the session-end hook) sets `released_at = now()` on the active draft.

## Not in the schema

- Rate-limiting key creation by IP happens in the API, not in a table.
- Local mode uses a SQLite database with the same shapes; see [local-storage.md](local-storage.md).
