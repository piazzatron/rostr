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

## Options considered

- **Move the API to Cloudflare Workers with one Durable Object per owner.** ~0.2s from anywhere, no duplicate names
  (an object handles one request at a time), one source of truth. But duration billing (each request keeps the
  object in memory ~10s) makes it ~$0.03–0.10 per heavy active user per month: ~$285 at 10,000 users, ~$3,200 at
  100,000, with a local draft cache. A rewrite in TypeScript, too.
- **Keep the server, add a KV cache at the edge that hands out a pre-picked next name** and confirms with the origin
  in the background. ~0.2s, 3–4x cheaper than Durable Objects, but two sources of truth and the same reconciliation
  as below, on a second system.
- **Keep the server, pre-pick on the user's machine.** Chosen, below. ~0ms on a machine that has drafted before,
  one server trip in a fresh cloud container (which is near the origin anyway, and takes seconds to start), a flat
  $10–50/month server at any scale, nothing new to deploy. A session even gets its name with the API down.

In the test, six sessions started at once got six different names from a Durable Object and six copies of one name
from a cache; started a second or more apart, the cache never handed out a duplicate. So pre-picking is safe when
sessions from different machines don't start within the same moment, which is the normal case, and the rare
collision is handled rather than prevented.

## The design

Cloud mode keeps a local cache in `~/.cache/rostr/cloud/` (see [local-storage.md](local-storage.md)):

1. **The next state.** Every draft-changing response from the API carries `next`: which roster each project uses,
   who's idle in each roster, and the auto-draft setting (also `GET /v1/next`). The CLI writes it to `next.json`.
   Registering, joining with a key, assembling, linking, and every other write refresh it, so a machine's very
   first session finds it warm.
2. **A fast draft.** The first-prompt hook (and resume, `/draft`, and the Codex equivalents) picks a member from the
   cached state itself and answers immediately: the session's last member when they're idle, else the member the
   server holds for this machine, else the first idle member. It then starts `rostr hook confirm-draft` detached, which calls `POST /v1/agents/draft` with the
   pick as `preferred`. The pick stays *pending* in `next.json` for 15 seconds, so the next fast draft on this
   machine skips it and a server draft made meanwhile sends it as `avoid`.
3. **Reservations, so machines don't announce the same name.** With every response that carries the next state,
   the server holds the member this machine will take next, per roster, for 24 hours (`X-Rostr-Machine`
   identifies the machine; see [api.md](api.md#reservations)). Other machines see that member as busy, so their
   caches never offer it. A collision now takes a cache more than 24 hours stale.
4. **Confirmation.** The server drafts the preferred member if they're free. If, with a stale cache, a session
   elsewhere took them first, it drafts their next copy (Ginny 2), so the session keeps a name it can recognize. A preferred member who was the
   session's previous member is a resume, old task included. The server's answer replaces the session's cached
   draft, and on the session's next prompt the hook retitles it and tells the agent its real name, once.
5. **The session cache.** A session's draft never changes while it's active, so `sessions/<session>.json` holds it
   and later prompts, `rostr task`, and compaction read it instead of calling. Releases mark it released (kept for
   the resume rule), and a server answer that the session isn't drafted clears it.
6. **Fallbacks.** No next state, a stale one (over a day old), an empty idle list, or a failed spawn means a server
   draft as before. A confirmation that failed (the API was down) is retried from the session cache on a later
   prompt, at most once a minute.

Per session, the server now hears: the confirmation (background), the hourly keep-alive, `rostr task`, and the
release. Nothing on the first prompt's critical path.

## What it doesn't do

- A fresh cloud container has no cache, so its first prompt is one server trip. Fine, since the container took
  seconds to start, and Claude Code's containers appear to be near the origin.
- A machine whose cache is more than 24 hours stale can announce a name another machine holds; that session
  becomes Ginny 2 on its next prompt.
- Nothing below ~0.16s per call for the calls that remain: that's the connection setup a fresh process pays.
