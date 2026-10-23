-- A machine's next pick in a roster, held so no other machine drafts that member first. The server writes one per
-- roster for a machine with every response that carries the next state; it expires on its own, and is deleted when
-- its member is drafted. member_name is the cast's spelling.
create table reservations (
  roster_id   uuid not null references rosters on delete cascade,
  machine_id  text not null,
  member_name text not null,
  expires_at  timestamptz not null,
  primary key (roster_id, machine_id)
);
