-- One row per rostr key. Only a SHA-256 hash of the derived auth token is stored.
create table owners (
  id                uuid primary key default gen_random_uuid(),
  auth_token_hash   text not null unique,
  default_roster_id uuid,
  draft_expiry      interval not null default '7 days',
  auto_draft        boolean not null default true,
  created_at        timestamptz not null default now()
);

-- a_team, b_team: [{"name": "Gandalf", "emoji": "🧙"}, ...], in draft order.
-- name_key is the name lowercased with accents stripped, so names are unique ignoring case and accents.
create table rosters (
  id         uuid primary key default gen_random_uuid(),
  owner_id   uuid not null references owners on delete cascade,
  name       text not null,
  name_key   text not null,
  emoji_mode text not null default 'on' check (emoji_mode in ('on', 'off', 'only')),
  a_team     jsonb not null,
  b_team     jsonb not null default '[]',
  created_at timestamptz not null default now(),
  constraint rosters_owner_name unique (owner_id, name_key)
);

alter table owners
  add foreign key (default_roster_id) references rosters on delete set null;

create table project_links (
  owner_id  uuid not null references owners on delete cascade,
  repo_hash text not null,
  repo      bytea not null,
  roster_id uuid not null references rosters on delete cascade,
  primary key (owner_id, repo_hash)
);

create index project_links_roster on project_links (roster_id);

create table drafts (
  id           bigint generated always as identity primary key,
  roster_id    uuid not null references rosters on delete cascade,
  member_name  text not null,
  member_emoji text not null,
  copy         int not null default 1,
  session_id   text not null,
  repo_hash    text,
  repo         bytea,
  task         bytea,
  drafted_at   timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  released_at  timestamptz
);

create unique index drafts_active_member on drafts (roster_id, member_name, copy) where released_at is null;
create unique index drafts_active_session on drafts (roster_id, session_id) where released_at is null;
create index drafts_session on drafts (session_id, drafted_at desc);
create index drafts_roster on drafts (roster_id, released_at);
