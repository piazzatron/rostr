# Drafting latency

How a session gets its name without waiting on the rostr API, and how that was decided.

## The problem

Every hook is a fresh process, and in cloud mode each call to `api.getrostr.sh` takes about 0.5s from Tokyo: ~0.16s
to set up the connection, then a trip to the origin in the US. The first-prompt hook used to make three calls in a
row (is the session drafted, is auto-draft on, draft), and later prompts one, so a local session waited 1.2–1.5s
before Claude could start. Measured on October 22, from a fresh `curl` per request, averaged over 10:

| | Total | Of which connection setup |
|---|---|---|
| Current API (`GET /health`) | 550ms | ~160ms |
| Plain Cloudflare Worker reply | 188ms | ~160ms |
| Worker + Durable Object pop | 219ms | ~160ms |
| Worker + KV cache read | 248ms | ~160ms |

## Options considered (October 22)

- **Move the API to Cloudflare Workers with one Durable Object per owner.** ~0.2s from anywhere, no duplicate names
  (an object handles one request at a time), one source of truth. But duration billing (each request keeps the
  object in memory ~10s) makes it ~$0.03–0.10 per heavy active user per month: ~$285 at 10,000 users, ~$3,200 at
  100,000, with a local draft cache. A rewrite in TypeScript, too.
- **Keep the server, add a KV cache at the edge that hands out a pre-picked next name** and confirms with the origin
  in the background. ~0.2s, 3–4x cheaper than Durable Objects, but two sources of truth and the same reconciliation
  as below, on a second system.
- **Keep the server, pre-pick on the user's machine.** Chosen at first; replaced the next day (below).

## The first design: pick on the machine, and why it went

Each machine cached its owner's "next state" (who's idle in each roster) from every API response. A hook picked a name
from it with no call, told the session, and confirmed with the server in the background, asking for that name. Two
machines picking from stale caches would announce the same name, so the server held a reservation for each machine
on its next pick, and a session whose pick was taken anyway became "Ginny 2" on its next prompt.

It made the first prompt about 15ms, but:

- **Collisions weren't rare without reservations.** Every cache and the server rank free members the same way, so two
  machines' stale caches usually offered the same name.
- **Reservations were most of the complexity,** and had bugs of their own: a reserved member couldn't be drafted by
  any other machine even when they were the last free name, nothing released a reservation but its 24-hour expiry, and
  a reservation could name a member a cast edit had since renamed.
- **The server was slow anyway.** A draft made 13 sequential database round trips (18 with reservations), at ~40ms
  each between Railway and Neon, so the background confirmation took about a second.

## The design now

The server decides every name, and the slow part is moved off the first prompt instead of guessed around.

1. **Draft when the session starts.** In cloud mode, `SessionStart` starts `rostr hook draft-ahead` in the
   background, which drafts the session and caches the draft. By the first prompt the name is usually waiting, and the
   first-prompt hook just reads it and tells the agent. If it isn't there yet, the prompt hook drafts itself; the
   server returns the same draft to both. See [cli-spec.md](cli-spec.md#drafting-ahead).
2. **Idle drafts rest.** A draft made at session start counts as a turn that ended at the draft, so a session opened
   and never prompted rests after 15 minutes and gives up its name to a new session that needs one.
3. **A draft is two database round trips:** one batch of reads, the pick in Go, one batch of writes. There's no
   database lock: unique indexes on active drafts stop two drafts taking one name, and the loser starts over. See
   [server-schema.md](server-schema.md#drafting).
4. **No auth lookup.** The owner's ID is the hash of their auth token, so a request knows its owner without a query.
5. **Expiry moved to a background sweep** that runs every 15 minutes, so requests don't do it.
6. **Auto-draft is per machine,** in a local file (or `ROSTR_AUTO_DRAFT`), so checking it needs no call.

The session's own draft is still cached (`~/.cache/rostr/cloud/sessions/`), so later prompts, `rostr task`, and
compaction make no call.

### Measured

Locally, with ~150ms added between the CLI and the API (about what a user sees through Cloudflare) and the database
either 40ms away (as Railway and Neon are today) or next to the API:

| | Database 40ms away | Database next to the API |
|---|---|---|
| A draft, before (13 round trips) | 695ms | 159ms |
| A draft, now (2 round trips) | 246ms | 164ms |
| First prompt, drafting then | 252ms | 169ms |
| First prompt, drafted when the session started | **14ms** | **13ms** |

Moving the API and the database into the same region still matters for everything else: each round trip between them
is most of a request's server time.

## What it doesn't do

- **No head start without a gap.** A session started with its prompt (`claude "fix the bug"`), or a cloud session
  whose container starts with the prompt already typed, has little or no time between `SessionStart` and the first
  prompt, so the first prompt waits for the draft: one API call.
- **Codex** drafts on its first prompt, one API call: its `SessionStart` fires only when the first prompt is sent
  (Codex 0.164.0, TUI and `codex exec`), so there's no head start to use.
- **Sessions opened and never prompted** hold a name for 15 minutes, until they rest, unless their `SessionEnd` runs
  first.
- **Connection setup** is still paid by every hook process that calls the API (DNS, TCP, and TLS to the nearest
  Cloudflare edge, ~180ms in one measurement).
