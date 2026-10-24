# Keep-alive and resting

How a session holds on to its name between prompts, and how it lets go. The rules are in
[cli-spec.md](cli-spec.md#resting) (Resting, Keep-alive, and step 1 of Drafting); this doc is the map from those rules
to the code, the files on disk, and the columns in the database, plus how to test it end to end.

## Three clocks per draft

Every active draft carries three times, in both stores (`drafts` in [local-storage.md](local-storage.md) and
[server-schema.md](server-schema.md)):

| Column | Set by | Decides |
|---|---|---|
| `last_seen_at` | Any draft of the same session, `rostr task`, every turn report, the hourly keep-alive | **Expiry:** a draft unseen for longer than the owner's expiry (`rostr config expiry`, 7 days by default) is benched, freeing its name |
| `turn_ended_at` | The `Stop` hook (and a title call that ends the turn); a draft made ahead of the first prompt starts with it set to the draft time | **Resting:** the session rests once this is at least 15 minutes old (`store.RestAfter`) and `turn_started_at` isn't later |
| `turn_started_at` | The next prompt or tool call after a turn ended | Ends the rest |

Resting doesn't free the name on its own. A resting session keeps its name until a new session finds every member busy:
the draft then takes the name of the member who has rested longest instead of drafting a numbered copy, benching
the resting draft. Expiry is the backstop for sessions that never fire `Stop` or `SessionEnd`.

## Hooks

Claude Code (`cli/internal/app/hook.go`); Codex does the same from `hook_codex.go`:

| Hook | What it does to the clocks |
|---|---|
| `UserPromptSubmit`, first prompt | Drafts (or, in cloud mode, reads the draft made at session start), marks the keep-alive file |
| `UserPromptSubmit`, later prompts | `laterPrompt`: `wakeSession` (turn start, see below), then `keepDraftAlive`, then the title sync |
| `PostToolUse` | `wakeAfterRest` without the check: reports the turn start in the background, if a turn end is pending |
| `Stop` (`turn-end`) | `recordTurnEnd`: writes `~/.cache/rostr/turns/<session>` and reports the end in the background (`rostr hook turn`) |
| `SessionEnd` | Removes both cache files and benches the draft |

### Keep-alive (`keepDraftAlive`)

At most once an hour per session (`keepalive.Interval`). The throttle is the modification time of
`~/.cache/rostr/keepalive/<session>` (`cli/internal/keepalive`), so a prompt inside the hour makes no store call at
all: no SQLite write locally, no API call in cloud mode. When due, it calls `store.Reclaim`, which is
`POST /v1/agents/keepalive` in cloud mode: bump the active draft, or if it was benched, take the same member back if
they're free. It never drafts a different member, so a session whose name was taken goes unnamed here.

In cloud mode, turn reports also bump `last_seen_at` on the server, so a session that ends turns regularly is kept
alive by those too; the hourly keep-alive matters for sessions whose `Stop` hook doesn't run.

### Coming back from a rest (`wakeAfterRest`)

Runs only when `~/.cache/rostr/turns/<session>` exists, meaning a turn ended and no start has been reported yet.

- **Within 10 minutes of the turn's end** (`wakeCheckAfter`, 15 minutes less a margin for clock skew), or from a tool
  call: report the start in the background and move on. Nothing can have taken the name yet.
- **Later, on a prompt:** report the start (`store.RecordTurn`) and wait. If the draft is still active, done. If it was
  benched (another session took the name, or it expired): `Reclaim` the old member if free, else draft a new one with
  the saved task. If the name changed, the prompt's output tells the agent (`claudecode.RenamedAfterRest`) and retitles
  the session.

The turn file is deleted only once a start has been reported, so a failed report is retried on the next prompt or tool
call.

## Expiry

- **Local:** lazy. Every store call that writes runs `expireStaleDrafts` first (`cli/internal/store/sqlite.go`).
- **Server:** a background sweep every 15 minutes (`sweepDrafts` in `server/internal/api/server.go`, interval in
  `server/main.go`). Until it runs, an expired draft still holds its name.

Benched drafts are kept for 7 days (`store.ReleasedRetention`) so a resumed session can ask for its old name back.

## Testing

Unit and integration coverage:

| Where | Covers |
|---|---|
| `cli/internal/keepalive` | The hourly throttle and the turn file |
| `cli/internal/store/sqlite_test.go` | Reclaim, expiry, keep-alive bumps preventing expiry (fake clock) |
| `server/internal/api/drafting_test.go`, `resting_test.go` | `POST /v1/agents/keepalive` and `/turn`, resting, taking a resting name |
| `cli/cmd/rostr/main_test.go` | The hooks in local mode, with the keep-alive file's mtime moved back |

End to end, `tests/` runs the real CLI against the real server and Postgres (`tests/run.sh`), moving timestamps in
Postgres and the cache files to stand in for time passing:

- `TestASessionOpenedButNeverPromptedGivesUpItsName`: a draft made at session start rests and is taken.
- `TestARestingSessionThatLostItsNameIsRenamedOnItsNextPrompt`: `Stop`, 20 minutes (moved back in `turn_ended_at`
  and the turn file), another session takes the name, and the next prompt renames the session and tells the agent.
- `TestThePromptHookKeepsADraftAliveAtMostOnceAnHour`: a prompt within the hour leaves `last_seen_at` alone; an hour
  later it bumps it and the session outlives the expiry; unbumped, the sweep benches it.

To poke at it by hand, start the stack (`docker compose up -d --wait`), point a sandboxed `HOME` at it with
`ROSTR_API_URL`, pipe hook JSON into `rostr hook claude-code <event>`, and move times with SQL such as:

```sql
update drafts set turn_ended_at = turn_ended_at - interval '20 minutes' where session_id = 'demo';
update drafts set last_seen_at = now() - interval '2 days' where session_id = 'demo' and benched_at is null;
```

A later prompt needs a `transcript_path` holding an earlier human prompt (a missing transcript counts as the first
prompt), and the turn file's `at` and the keep-alive file's mtime have to move back along with the database.
