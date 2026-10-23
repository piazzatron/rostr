-- The owner's ID becomes the SHA-256 hash of their auth token, so a request knows its owner without a lookup.
-- Auto-draft moves to each machine, reservations go, and expiry moves to a background sweep. There's no production data
-- worth keeping, so the tables are rebuilt rather than migrated.
drop table if exists reservations, drafts, project_links, rosters, owners cascade;

-- One row per rostr key. id is the hex SHA-256 of the derived auth token.
create table owners (
  id                text primary key,
  default_roster_id uuid,
  draft_expiry      interval not null default '7 days',
  created_at        timestamptz not null default now()
);

-- a_team, b_team: [{"name": "Gandalf", "emoji": "🧙"}, ...], in draft order.
-- name_key is the name lowercased with accents stripped, so names are unique ignoring case and accents.
create table rosters (
  id         uuid primary key default gen_random_uuid(),
  owner_id   text not null references owners on delete cascade,
  name       text not null,
  name_key   text not null,
  emoji_mode text not null default 'on' check (emoji_mode in ('on', 'off', 'only')),
  a_team     jsonb not null,
  b_team     jsonb not null default '[]',
  created_at timestamptz not null default now(),
  constraint rosters_owner_name unique (owner_id, name_key)
);

alter table owners add foreign key (default_roster_id) references rosters on delete set null;
create index owners_default_roster on owners (default_roster_id);

create table project_links (
  owner_id  text not null references owners on delete cascade,
  repo_hash text not null,
  repo      bytea not null,
  roster_id uuid not null references rosters on delete cascade,
  primary key (owner_id, repo_hash)
);

create index project_links_roster on project_links (roster_id);

create table drafts (
  id              bigint generated always as identity primary key,
  owner_id        text not null references owners on delete cascade,
  roster_id       uuid not null references rosters on delete cascade,
  member_name     text not null,
  member_emoji    text not null,
  copy            int not null default 1,
  session_id      text not null,
  repo_hash       text,
  repo            bytea,
  task            bytea,
  drafted_at      timestamptz not null default now(),
  last_seen_at    timestamptz not null default now(),
  benched_at      timestamptz,
  turn_started_at timestamptz,
  turn_ended_at   timestamptz
);

-- No two active drafts hold the same name, and a session has at most one active draft across its owner's rosters
create unique index drafts_active_member on drafts (roster_id, member_name, copy) where benched_at is null;
create unique index drafts_active_session on drafts (owner_id, session_id) where benched_at is null;
-- A session's drafts, newest first (resume), and the owner's drafts (the cascade when an owner is deleted)
create index drafts_owner_session on drafts (owner_id, session_id, drafted_at desc);
-- A roster's drafts (the cascade when a roster is disbanded), and the background sweep's two passes
create index drafts_roster on drafts (roster_id);
create index drafts_active_last_seen on drafts (last_seen_at) where benched_at is null;
create index drafts_benched on drafts (benched_at) where benched_at is not null;
