-- One active draft per session across all of an owner's rosters, enforced by the database. Drafts carry their
-- owner so a partial unique index can cover every roster. There's no production data yet, so nothing is backfilled.
alter table drafts add column owner_id uuid not null references owners on delete cascade;

drop index drafts_active_session;
create unique index drafts_active_session on drafts (owner_id, session_id) where released_at is null;
