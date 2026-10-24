-- New owners' drafts expire after a day unseen. Existing owners keep the expiry they have.
alter table owners alter column draft_expiry set default '1 day';
