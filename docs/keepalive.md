# Keep-alive and resting

How a session holds on to its name between prompts, and how it lets go.

## Three clocks per draft

Every active draft carries three times, in both stores (see [storage.md](storage.md)):

| Column | Set by | Decides |
|---|---|---|
| `last_seen_at` | A re-draft of the same session, `rostr task`, every turn report, the hourly keep-alive | **Expiry:** a draft unseen for longer than the owner's expiry (`rostr config expiry`, 7 days by default) is benched |
| `turn_ended_at` | The `Stop` hook, or a title call that ends the turn. In cloud mode a draft made ahead of the first prompt starts with it set | **Resting:** the session rests once this is at least 15 minutes old (`store.RestAfter`) and `turn_started_at` isn't later |
| `turn_started_at` | The next prompt or tool call after a turn ended | Ends the rest |

## Resting

A session that finished its turn and hasn't been prompted for 15 minutes rests. It keeps its name and shows under
`Resting:` in `rostr stats`, but when a new session finds every member busy, it takes the name of the member who has
rested longest instead of drafting a numbered copy. Fanning out reuses the names of sessions you've left, while a
session you come back to soon keeps its name. Sessions that never fire `SessionEnd` (a closed desktop session, an
archived cloud session) rest rather than hold a name until expiry.

Turn reports (`POST /v1/agents/turn` in cloud mode) run in the background and only move each time forward, so
reports arriving out of order can't mark a working session as resting.

### Coming back from a rest (`wakeAfterRest`)

The `Stop` hook saves the turn's end and the session's name to `~/.cache/rostr/turns/<session>`. The next prompt or
tool call reads it:

- **Within 10 minutes of the turn's end** (15 less a margin for clock skew), or from a tool call: report the start in
  the background. Nothing can have taken the name yet.
- **Later, on a prompt:** report the start and wait. If the draft was benched (its name taken, or expired), reclaim
  the old member if free, else draft a new one with the saved task. If the name changed, the agent is told
  (`claudecode.RenamedAfterRest`) and the session retitled.

The turn file is deleted only once a start has been reported, so a failed report is retried next time.

## Keep-alive (`keepDraftAlive`)

On later prompts, at most once an hour per session (`keepalive.Interval`). The throttle is the modification time of
`~/.cache/rostr/keepalive/<session>` (`cli/internal/keepalive`), so a prompt inside the hour makes no keep-alive call.
When due, it calls `store.Reclaim` (`POST /v1/agents/keepalive` in cloud mode): bump the active draft, or if it was
benched, take the same member back if free. It never drafts a different member.

Turn reports bump `last_seen_at` too, so the hourly keep-alive mainly matters for sessions whose `Stop` hook doesn't
run.

## Expiry

- **Local:** lazy. `expireStaleDrafts` (`cli/internal/store/sqlite.go`) runs at the start of drafts, turn reports,
  task changes, benches, and stats.
- **Server:** `sweepDrafts` (`server/internal/api/server.go`) every 15 minutes per instance. Until it runs, an expired
  draft still holds its name.

Benched drafts are kept for 7 days (`store.ReleasedRetention`) so a resumed session can ask for its old name back, then
deleted. `rostr bench <name>` frees a dead session's name sooner.

## Testing

| Where | Covers |
|---|---|
| `cli/internal/keepalive` | The hourly throttle and the turn file |
| `cli/internal/store/sqlite_test.go` | Reclaim, expiry, keep-alive bumps preventing expiry (fake clock) |
| `server/internal/api/drafting_test.go`, `resting_test.go` | `POST /v1/agents/keepalive` and `/turn`, resting, taking a resting name |
| `cli/cmd/rostr/main_test.go` | The hooks in local mode, with the keep-alive file's mtime moved back |
| `tests/e2e_test.go` | The real CLI against the real server, moving timestamps in Postgres and the cache files to stand in for time passing |

The end-to-end tests (`tests/run.sh`) include a session drafted ahead that's never prompted giving up its name, a
resting session that lost its name being renamed on its next prompt, and the prompt hook bumping `last_seen_at` at
most once an hour.

To poke at it by hand, start the stack (`docker compose up -d --wait`), point a sandboxed `HOME` at it with
`ROSTR_API_URL`, pipe hook JSON into `rostr hook claude-code <event>`, and move times with SQL:

```sql
update drafts set turn_ended_at = turn_ended_at - interval '20 minutes' where session_id = 'demo';
update drafts set last_seen_at = now() - interval '2 days' where session_id = 'demo' and benched_at is null;
```

A later prompt needs a `transcript_path` holding an earlier human prompt (a missing transcript counts as the first
prompt), and the turn file's `at` and the keep-alive file's mtime have to move back along with the database.
