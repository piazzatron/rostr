-- Drafts are benched, matching the CLI's `bench` command.
alter table drafts rename column released_at to benched_at;
