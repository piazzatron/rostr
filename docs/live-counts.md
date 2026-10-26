# Live counts

The home page's live board hangs from the top of the screen, fixed in the right margin on screens at least 1360px
wide: `working` in big split-flap digits with a pulsing red light in the corner, "agents at work" under them, then "4
on the bench" (`benched`). A wire runs from the top edge of the screen to the middle of its top. The board waits out
of sight and drops 0.6 seconds after load: it falls until the wire goes taut, bobs on the wire's stretch, and swings
out from the page and back in depth like a sheet of paper, each swing smaller and none hitting the page, until it
rests flat against it, its shadow on the page following it. Scrolling down reels it back up out of sight, and it drops
again once the visitor is back at the top. Its digits turn in from blank just after the jolt, and after that each
digit turns forward one flap at a time to its new value. The number is zero-padded to two digits; past 99 it gains
flaps, which shrink as they're added (with a gap where the thousands comma would go), so the board stays within the
margin up to six digits.

There's also a live bar, "LIVE 128 agents drafted out of 1,530", pinned to the bottom of the screen; it slides up once
the visitor scrolls down and crawls slowly across. It's off for now (`SHOW_LIVE_BAR` in `site/src/pages/index.astro`).

`working` is the number of active drafts, copies included (an expired draft counts until the sweep benches it).
`benched` is the number of idle members in every roster: cast members whose name (copy 1) is free. Both come from one
query in `server/internal/api/counts.go`. The socket is the only way to get them; there's no polling endpoint.

## Flow

```
API server write ──▶ POST /update ──▶ LiveCounts Durable Object ──▶ every open /ws socket
   (Postgres)        (shared secret)   (stores the counts)            (browsers on getrostr.sh)
```

1. **A write commits.** Registrations, drafts, keep-alives, every write that runs in `withOwnerTx` (benches, task
   changes, roster creates, edits, cast replacements, disbands, settings, links), and every run of the background
   sweep that benches a draft ask for a push. Turn reports don't, since they can't change the counts.
2. **Pushes are coalesced.** The first write schedules a push 500ms later; writes in that window ride along. The push
   recomputes the counts with a single query and sends `POST {LIVE_URL}/update`, both within 3 seconds. It runs in the
   background, so a slow or failing Worker never delays a write; failures are logged and the next write tries again.
   Pushes from one instance go out one at a time, so the Worker never gets an older count after a newer one.
3. **The Worker stores and broadcasts.** The `rostr-live` Worker ([`live/`](../live/)) checks the secret and passes
   the counts to its one Durable Object, which saves them and sends them to every connected socket. The Worker has
   just two routes, `GET /ws` and `POST /update`.
4. **Browsers update in place.** The home page opens `{PUBLIC_LIVE_URL}/ws`, gets the current counts on connect, and
   counts up or down to each new pair. If the socket fails or closes, it retries after 5s, 10s, 30s, then every 60s.
   With no drafted agents (or no counts yet), the board and the bar stay hidden.

A draft that passes its owner's expiry stops counting when the sweep benches it, within 15 minutes.

## Configuration

| Where | Variable | What |
|---|---|---|
| Worker | `LIVE_SECRET` (secret, `wrangler secret put`) | The shared secret `POST /update` requires |
| API server | `LIVE_URL` | The Worker's base URL, `https://live.getrostr.sh` |
| API server | `LIVE_SECRET` | The same secret. With `LIVE_URL` or `LIVE_SECRET` unset, nothing is pushed |
| Site (build time) | `PUBLIC_LIVE_URL` | The Worker's base URL. Unset, the page has no live board or bar |

## Why one Durable Object

- **One broadcaster.** Every API instance pushes to the same object (`idFromName("global")`), and every browser
  connects to it, so there's no fan-out between objects and no question of which one is current.
- **Hibernated sockets.** The object accepts sockets with the WebSocket hibernation API, so between pushes it's
  evicted from memory while the sockets stay open. An idle tab costs nothing; the object only runs for a moment per
  push and per new connection.
- **Bounded sockets.** Since every visitor shares the object, it refuses sockets past 10 per IP or 10,000 in all, and
  an alarm every 10 minutes closes sockets open over an hour; the page reconnects 5 seconds later.
- **Cost rounds to zero.** Pushes are at most two a second per API instance and usually far fewer, each one request
  and a few milliseconds of duration. One object handles thousands of connected sockets, far more than the home page
  has visitors at once.
