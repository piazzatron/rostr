# Cloud mode and the API

## Keys

There are no accounts: one rostr key (`rostr_sk_` + 32 random bytes, base64url) owns a person's whole cloud setup. The
CLI generates it and never sends it. From it, `cli/internal/crypto` derives three keys with HKDF-SHA256 (info labels
`auth`, `encrypt`, `fingerprint`):

| Key | Used for |
|---|---|
| Auth token | The bearer token for every request. The server stores only its SHA-256, which is the owner's ID |
| Encryption key | AES-256-GCM over tasks and repo names, so the server can't read them |
| Fingerprint key | HMAC-SHA256 of the lowercased `owner/repo`, so the server can match project links without repo names |

Plaintext on the server: rosters, who's drafted and when, session IDs. There's no recovery or rotation: a lost key is
a lost setup. `rostr config key` prints the key (terminal only, so it doesn't land in an agent's transcript), and
`--token` prints the auth token.

## API

`server/` serves `https://api.getrostr.sh` (JSON over HTTPS, routes in `server/internal/api/server.go`). Errors are
`{"error": {"code", "message"}}`; branch on `code`.

| Routes | What |
|---|---|
| `GET /health` | Database ping, no auth |
| `POST /v1/register` | Creates an owner, optionally importing a local setup (`rostr cloud on`). No auth; rate-limited per IP |
| `GET /v1/stats` | Every roster with its drafted, resting, and idle members. **Public and stable**, CORS `*` |
| `/v1/rosters/{name}` (+ `/cast`) | Create, read, edit, replace the cast, disband |
| `/v1/settings`, `/v1/links/{repoHash}` | Default roster and expiry; project links |
| `POST /v1/agents/{draft,keepalive,bench,task,turn}` | The session lifecycle (see [drafting.md](drafting.md), [keepalive.md](keepalive.md)) |
| `GET /v1/sessions/{session}` | A session's active draft, without bumping it |

Every route except `/v1/stats` is internal: only the CLI calls it, and it changes with the CLI. Requests are
rate-limited per client IP (1,200/minute by default), then per auth token (300/minute), in memory per instance. Each
owner can have at most 100 rosters and 1,000 project links, imports included.

`GET /v1/stats` returns the same shape as `rostr stats --json`, with each agent's `task` and `repo` as ciphertext.
Integrations decrypt them with the key derivation above; the site gives the recipe.

## Server background work

- **Sweep** (every 15 minutes per instance): benches drafts past their owner's expiry, deletes drafts benched over 7
  days ago.
- **Live push:** after writes, pushes fresh counts to the live Worker (see [live-counts.md](live-counts.md)).

Environment and local development are in [`server/README.md`](../server/README.md).
