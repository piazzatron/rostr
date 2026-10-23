-- When the session's current or last turn started and ended, as the client reported them. A draft whose turn ended
-- (after it last started) more than 15 minutes ago is resting: a draft that would otherwise get a numbered copy takes
-- the longest-resting member instead.
alter table drafts add column turn_started_at timestamptz, add column turn_ended_at timestamptz;
