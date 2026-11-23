-- Drafts expire after two weeks unseen. Owners on one day, the old default, move to two weeks too (one day chosen on
-- purpose can't be told apart from the default); any other expiry stays.
alter table owners alter column draft_expiry set default '14 days';
update owners set draft_expiry = '14 days' where draft_expiry = '1 day';
