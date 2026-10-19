# Server schema

The rostr API (`api.getrostr.sh`) stores cloud rosters in Postgres. Local rosters never touch it.

Everything an owner has hangs off their key. There are no accounts: the key is the identity.

```sql
-- One row per rostr key. The key itself is never stored, only its SHA-256 hash.
create table owners (
  id                uuid primary key default gen_random_uuid(),
  key_hash          text not null unique,
  default_roster_id uuid,  -- set after the first roster is created
  created_at        timestamptz not null default now()
);

-- A roster and all its members. Members are always read and written as a whole (`rostr edit` opens the entire
-- roster), so they live in one JSON column. Array order is draft order; the first six are the A team by default.
-- members: [{"name": "Gandalf", "emoji": "🧙", "team": "A"}, ...]
create table rosters (
  id            uuid primary key default gen_random_uuid(),
  owner_id      uuid not null references owners on delete cascade,
  name          text not null,
  emoji_enabled boolean not null default true,
  members       jsonb not null,
  created_at    timestamptz not null default now(),
  unique (owner_id, name)
);

alter table owners
  add foreign key (default_roster_id) references rosters on delete set null;

-- Which roster a project uses (`rostr use`). Projects without a link use the owner's default roster.
create table project_links (
  owner_id  uuid not null references owners on delete cascade,
  repo      text not null,  -- "owner/repo", from the git remote
  roster_id uuid not null references rosters on delete cascade,
  primary key (owner_id, repo)
);

-- One row per draft. Rows are kept after release so a resumed session can ask for its old name back.
create table drafts (
  id           bigint generated always as identity primary key,
  roster_id    uuid not null references rosters on delete cascade,
  member_name  text not null,
  session_id   text not null,
  repo         text,
  task         text,          -- set by `rostr draft --task` or `rostr assign`
  drafted_at   timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),  -- bumped by re-drafts and `rostr assign`
  released_at  timestamptz    -- null while the agent is active
);

-- A member can only be drafted once at a time, and a session can only hold one member at a time.
create unique index drafts_active_member on drafts (roster_id, member_name) where released_at is null;
create unique index drafts_active_session on drafts (roster_id, session_id) where released_at is null;

-- For resuming: find the member a session had before.
create index drafts_session on drafts (session_id, drafted_at desc);
```

## Drafting

`rostr draft` runs in one transaction:

1. **Lock the roster row** (`select ... from rosters where id = $1 for update`). This serializes drafts within one
   roster, so two sessions starting at once can't both pick Gandalf. Contention is per roster, which means one
   person's own sessions, so the lock is never busy for long.
2. **Expire stale drafts:** release any active draft whose `last_seen_at` is older than 48 hours. This is the backstop
   for sessions that die without running the release hook. It runs here, lazily, so there's no cleanup job.
3. **Same session, same name:** if the session already has an active draft, bump `last_seen_at` and return it.
4. **Resume:** if the session had a member before and that member is free, draft them again.
5. **Otherwise** draft the first free member in roster order, which drafts the A team before the B team.

The unique indexes back this up: if the logic ever gets it wrong, the insert fails instead of drafting a duplicate.

## Releasing

`rostr release` (and the session-end hook) sets `released_at = now()` on the active draft.

## Not in the schema

- Rate-limiting key creation by IP happens in the API, not in a table.
- Local rosters use the same shapes, stored as files on disk.
