-- A roster's cast is one list in draft order: members: [{"name": "Gandalf", "emoji": "🧙"}, ...]. Existing rosters
-- keep their members, in the order they were drafted before.
alter table rosters add column members jsonb;
update rosters set members = a_team || b_team;
alter table rosters alter column members set not null, drop column a_team, drop column b_team;
