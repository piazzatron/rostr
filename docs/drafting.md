# Drafting

How a session gets its name. Local: `Draft` in `cli/internal/store/sqlite.go`. Server: `draftSession` in
`server/internal/api/agents.go`.

## The pick

1. **Same session, same name.** If the session has an active draft, bump its `last_seen_at` and return it. Drafting
   is idempotent per session.
2. **Resume.** If the session had a member before, that member is still in the cast, and the name is free, draft them
   again from the same roster. Otherwise the session needs a new member, from the `--roster` flag, else the linked
   roster of the project it's in now, else the default.
3. **Least recently drafted.** Otherwise draft the free member drafted longest ago (never-drafted first, then roster
   order), so drafts cycle through the cast.
4. **Take a resting name.** If every member is busy, take the name of the member whose session has rested longest
   (see [keepalive.md](keepalive.md#resting)), benching that draft.
5. **Copies.** If no one is resting either, draft a numbered copy (`🧙 Gandalf 2`, then `💍 Frodo 2`, …). A session
   always gets a name.

`POST /v1/agents/keepalive` and compaction run steps 1-2 only: they never pick a different member.

With auto-draft off, only step 1 runs. Member names match ignoring case and accents throughout. Local and server
follow the same rules; only expiry differs (lazy locally, a 15-minute sweep on the server; see
[keepalive.md](keepalive.md#expiry)).

## Concurrency

- **Local:** each draft runs in one `begin immediate` SQLite transaction, so two sessions starting at once draft one
  after the other.
- **Server:** no database lock. A draft is two round trips: one batch of reads, the pick in Go, one batch of writes.
  Unique indexes on active drafts (one per name, one per session) make the loser of a race fail its insert, and it
  retries (up to 10 times, with a short random wait). Drafts for one owner on one instance also take turns through
  an in-process mutex, so a burst of sessions doesn't keep colliding. The owner's ID is the hash of their auth token,
  so no auth lookup is needed.

## Drafting ahead

In cloud mode each API call costs ~0.5s from far away, so the first prompt shouldn't wait on one. `SessionStart`
(`startup`, `clear`) starts `rostr hook draft-ahead` in the background, which drafts the session and saves the draft in
`~/.cache/rostr/cloud/sessions/<session>.json`. The first-prompt hook reads it from there, which takes ~15ms.

- The draft is marked idle: it counts as a turn that ended at the draft, so a session opened and never prompted rests
  after 15 minutes and its name can be taken. Its first prompt then reports the turn start like any prompt after a
  rest.
- If the first prompt comes before the draft finishes, the prompt hook drafts itself and the server returns the same
  draft to both.
- Later prompts, `rostr task`, and compaction read the session cache too (trusted for an hour), so they make no draft
  call.
- Local mode has no network to wait on, and Codex fires `SessionStart` with the first prompt, so both draft on the
  first prompt.

An earlier design picked names on the user's machine from a cached "who's free" list and confirmed in the background.
It needed per-machine reservations to avoid collisions and was replaced: the server decides every name.
