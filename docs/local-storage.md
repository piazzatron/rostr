# Local storage

In local mode (the default, when there's no rostr key), everything lives in plain files, so there's no database to
install. The files use the same shapes as the [server schema](server-schema.md), which makes `rostr config cloud on` a
straight copy. In cloud mode everything lives on the API instead, and only `credentials` is kept on disk.

Everything is under `~/.config/rostr`.

```
rostr/
  config.json                  default roster and project links
  credentials                  your cloud key (only readable by you)
  rosters/
    work.json                  one file per roster
  drafts/
    work/
      gandalf.json             one file per active draft
  sessions/
    <session-id>.json          the last member each session had, for resuming
```

## Files

**`config.json`**

```json
{
  "defaultRoster": "work",
  "projectLinks": { "piazzatron/rostr": "work" }
}
```

**`rosters/<roster>.json`.** `rostr edit` opens this file in your editor.

```json
{
  "emojiMode": "on",
  "members": [{ "name": "Gandalf", "emoji": "🧙", "team": "A" }]
}
```

**`drafts/<roster>/<member>.json`.** Exists only while that member is drafted.

```json
{
  "sessionId": "…",
  "repo": "piazzatron/rostr",
  "task": "Fix auth redirect loop",
  "draftedAt": "2026-10-19T03:12:00Z",
  "lastSeenAt": "2026-10-19T03:12:00Z"
}
```

**`sessions/<session-id>.json`.** `{ "roster": "work", "member": "Gandalf" }`. Files older than 7 days are deleted.

## Concurrency without a lock

Several sessions can start at the same moment, so drafting can't read, change, and rewrite a shared file. Instead,
each draft is its own file:

- **Drafting** creates `drafts/<roster>/<member>.json` in create-only mode (`O_EXCL`). The
  OS guarantees only one process can create a given file, so the first session to create `gandalf.json` gets Gandalf
  and any other gets "already exists" and tries the next member.
- **Releasing** deletes the draft file and writes `sessions/<session-id>.json`.
- **`rostr agent task`** rewrites only its own draft file, which no other session touches.

Files that people edit (`config.json`, roster files) are written to a temporary file and renamed into place, so
nothing ever reads a half-written file.

## Drafting

1. **Expire stale drafts:** delete draft files whose `lastSeenAt` is older than 48 hours. If two sessions delete the
   same stale file, the second delete just fails, which is fine.
2. **Same session, same name:** if a draft file in this roster already has this session's ID, bump `lastSeenAt` and
   return it.
3. **Resume:** if `sessions/<session-id>.json` names a member, try to create their draft file first.
4. **Otherwise** try to create a draft file for each member in roster order, A team first, and stop at the first one
   that succeeds.

## Moving to the cloud

`rostr config cloud on` uploads every roster, the default roster, project links, and active drafts, creates your key, and
then deletes the local files except `credentials`. `rostr config cloud off` downloads everything back and deletes the
key. A machine is in cloud mode whenever it has a key, from `credentials` or the `ROSTR_KEY` environment variable.
