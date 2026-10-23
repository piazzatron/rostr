# rostr live

The Cloudflare Worker behind the home page's live "agents working / on the bench" line. One Durable Object holds the
counts; the API server pushes new counts to it after writes, and it sends them to every open browser socket. The
whole flow is in [docs/live-counts.md](../docs/live-counts.md).

Routes:

- `GET /ws`: a WebSocket. It sends the current counts on connect, then every update, as
  `{"working": n, "benched": m}`. Anything the browser sends closes it.
- `POST /update`: `{"working": n, "benched": m}` with `Authorization: Bearer <LIVE_SECRET>`. Stores the counts and
  sends them to every socket. `400` for bad counts, `401` for a bad secret.

## Develop

```sh
npm install
npm test                                            # the routes, in the Workers runtime
npm run build                                       # typecheck and `wrangler deploy --dry-run`
npx wrangler dev --var LIVE_SECRET:devsecret        # locally, on localhost:8787
```

Run `npm run types` after changing `wrangler.toml`, to regenerate `worker-configuration.d.ts`.

## Testing locally

The whole flow on one machine: the Worker, the API server pushing to it, and a site build that connects to it. Start
the compose Postgres first (see [server/README.md](../server/README.md)) and run each command from the repo root.

```sh
# 1. The Worker, on localhost:58787
(cd live && npx wrangler dev --port 58787 --var LIVE_SECRET:devsecret)

# 2. The API server, on localhost:58123, pushing to the Worker after each write
(cd server && DATABASE_URL='postgres://rostr:rostr@localhost:55433/rostr?sslmode=disable' PORT=58123 \
  LIVE_URL=http://localhost:58787 LIVE_SECRET=devsecret go run .)

# 3. The site, connecting to the Worker; serve site/dist with any static server
(cd site && PUBLIC_LIVE_URL=http://localhost:58787 npm run build)
```

Writes to the API (register, `rostr assemble`, drafts, releases) then show up on the page about half a second later.

## Deploy

```sh
npx wrangler secret put LIVE_SECRET   # a long random string; the API server gets the same one
npx wrangler deploy                   # to https://live.getrostr.sh (a custom domain on the getrostr.sh zone)
```

Then point the other two pieces at it:

- **API server:** set `LIVE_URL` to `https://live.getrostr.sh` and `LIVE_SECRET` to the same secret.
- **Site:** set `PUBLIC_LIVE_URL` to the same URL when building `site/`. Without it, the home page has no live line.
