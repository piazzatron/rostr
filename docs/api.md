# rostr API

The HTTP API behind cloud mode, at `https://api.getrostr.sh`. Behavior is in [cli-spec.md](cli-spec.md) and storage
in [server-schema.md](server-schema.md). The server lives in [`server/`](../server/).

**Stability.** `GET /v1/stats` is **public and stable**: dashboards and integrations can rely on it. Every other
endpoint is **internal**: only the CLI calls it, and it can change with the CLI.

## Conventions

- **Transport:** JSON over HTTPS, UTF-8. Request bodies are JSON objects (max 1 MiB); unknown fields are rejected with
  `400`. Responses are `application/json`.
- **Auth:** `Authorization: Bearer <auth token>` on every endpoint except `POST /v1/register` and `GET /health`. The
  auth token is the 32-byte HKDF output with info `auth` (see [Cloud and keys](cli-spec.md#cloud-and-keys)),
  base64url without padding (43 characters). The server stores only the hex SHA-256 of the 32 decoded bytes, which is
  the owner's ID, so a request needs no lookup to know its owner. A token with no owner (never registered, or its owner
  disbanded their last roster) gets `401`.
- **Ciphertext** (marked 🔒 below): AES-256-GCM output, standard base64 with padding: the 12-byte nonce, then the
  ciphertext and 16-byte tag. Decoded length must be 28 bytes to 16 KiB. The server stores and returns these bytes
  as-is and never decrypts them.
- **Fingerprints** (`repoHash`): HMAC-SHA256 of the lowercased `owner/repo` with the fingerprint key, as 64
  lowercase hex characters.
- **Names:** roster and member names match ignoring case and accents (`eowyn` finds `Éowyn`), in paths and bodies.
  Names are trimmed and 1 to 64 characters. Responses always give the stored spelling.
- **Times:** RFC 3339 in UTC.
- **Durations** (`draftExpiry`): a whole number of hours, days, or weeks: `48h`, `7d`, `2w`, from `1h` to `52w`. The
  server returns whole days when it can, otherwise hours: `48h` comes back as `2d`, `2w` as `14d`, `36h` as `36h`.
- **Path parameters** are URL-encoded (`/v1/rosters/Smash%20Bros`).

## Errors

Every error has the same shape:

```json
{ "error": { "code": "roster_not_found", "message": "No roster named \"work\"." } }
```

`message` is for people; branch on `code`.

| Status | Code | When |
|---|---|---|
| 400 | `invalid_request` | Malformed JSON, unknown or missing fields, invalid values |
| 401 | `unauthorized` | Missing, malformed, or unregistered auth token |
| 404 | `not_found` | No such endpoint (including a known path with the wrong method) |
| 404 | `roster_not_found` | A named roster doesn't exist |
| 404 | `link_not_found` | The project isn't linked |
| 404 | `not_drafted` | No active draft matches the session or name |
| 404 | `no_rosters` | Drafting with no roster given, no link, and no default (the owner has no rosters) |
| 409 | `already_registered` | Registering a token that's already registered |
| 409 | `roster_exists` | Creating a roster whose name matches an existing one |
| 409 | `member_drafted` | An edit renames or removes a drafted member |
| 409 | `ambiguous_name` | Releasing or tasking by a name drafted in more than one roster; the error also has `"rosters": ["work", "home"]` |
| 409 | `conflict` | The write conflicts with existing data (for example, an import with two drafts of the same member) |
| 413 | `too_large` | Body over 1 MiB |
| 429 | `rate_limited` | Rate limit hit; `Retry-After` gives the seconds to wait |
| 500 | `internal` | Server bug |
| 503 | `unavailable` | `GET /health` only: the database is unreachable |

## Rate limits

In-memory, per server instance, in fixed windows:

- **Registration:** 20 per hour per client IP (`RATE_LIMIT_REGISTRATIONS_PER_HOUR`).
- **Everything else:** 300 per minute per auth token (`RATE_LIMIT_REQUESTS_PER_MINUTE`).

Over the limit, the response is `429 rate_limited` with `Retry-After`.

## CORS

`GET /v1/stats` allows any origin: its responses, including errors, carry `Access-Control-Allow-Origin: *`, and
`OPTIONS /v1/stats` answers preflights with `204`, `Access-Control-Allow-Methods: GET, OPTIONS`,
`Access-Control-Allow-Headers: Authorization, Content-Type`, and `Access-Control-Max-Age: 86400`. No other endpoint
sends CORS headers, so browsers can't call them.

## Objects

**Member**

```json
{ "name": "Gandalf", "emoji": "🧙" }
```

`emoji` is required (1 to 64 bytes). Members within a roster can't match each other by name.

**Roster** (internal endpoints)

```json
{
  "name": "work",
  "emojiMode": "on",
  "aTeam": [{ "name": "Gandalf", "emoji": "🧙" }],
  "bTeam": [{ "name": "Boromir", "emoji": "🛡️" }],
  "isDefault": true,
  "activeDrafts": 2,
  "linkedProjects": 1,
  "createdAt": "2026-10-20T09:57:14Z"
}
```

- `emojiMode` is `on`, `off`, or `only`. It defaults to `on` when a request leaves it out.
- `aTeam` needs at least one member. A roster has at most 200 members.
- `activeDrafts` and `linkedProjects` are the counts `rostr disband` confirms with.

**Draft**: an agent drafted for a session.

```json
{
  "name": "Gandalf",
  "emoji": "🧙",
  "copy": 2,
  "team": "a",
  "roster": "work",
  "emojiMode": "on",
  "session": "5f0c…",
  "task": "🔒 base64 | null",
  "repo": "🔒 base64 | null",
  "draftedAt": "2026-10-20T09:57:14Z"
}
```

- `name` is the member's name. `copy` is 1, or 2 and up for numbered copies, displayed as `Gandalf 2`.
- `emoji` is stored on the draft, so it survives a cast replacement.
- `team` is `"a"` or `"b"`, or `null` when the member is no longer in the roster's cast (it was replaced while they
  were drafted).
- `emojiMode` is the roster's, so the CLI can build `display` without another call.

**Settings**

```json
{ "defaultRoster": "work", "draftExpiry": "7d" }
```

`defaultRoster` is `null` when there are no rosters. Auto-draft isn't here: it's a setting of each machine, kept by the
CLI (see [`rostr config`](cli-spec.md#rostr-config)).

**Link**: a project's roster.

```json
{ "repoHash": "3f2a…64 hex", "repo": "🔒 base64", "roster": "work" }
```

## Endpoints

| Method and path | Auth | CLI use |
|---|---|---|
| `GET /health` | none | Deploy and compose healthchecks |
| `POST /v1/register` | none | `cloud on` (new setup), `assemble --cloud` |
| `GET /v1/stats` | bearer, **public** | `stats`, every roster's cast, finding an agent by name (`bench`, `task`), dashboards |
| `POST /v1/rosters` | bearer | `assemble` (new roster) |
| `GET /v1/rosters/{name}` | bearer | Reading a roster's cast and counts: `edit`, `disband` confirmation, `config emoji` |
| `PUT /v1/rosters/{name}` | bearer | `edit`, `config emoji` |
| `PUT /v1/rosters/{name}/cast` | bearer | `assemble` (replace a cast) |
| `DELETE /v1/rosters/{name}` | bearer | `disband` |
| `GET /v1/settings` | bearer | `config`; checking a pasted key |
| `PATCH /v1/settings` | bearer | `config default`, `config expiry` |
| `GET /v1/links` | bearer | Listing links |
| `GET /v1/links/{repoHash}` | bearer | Bare `link`; the project's roster for `edit` and `config emoji` |
| `PUT /v1/links/{repoHash}` | bearer | `link` |
| `DELETE /v1/links/{repoHash}` | bearer | `unlink` |
| `POST /v1/agents/draft` | bearer | `draft`, drafting a session when it starts, the draft step, resume, `/draft` |
| `POST /v1/agents/keepalive` | bearer | Prompt-hook keep-alive, compaction, resume with auto-draft off |
| `POST /v1/agents/bench` | bearer | `bench`, `SessionEnd` |
| `POST /v1/agents/task` | bearer | `task` |
| `POST /v1/agents/turn` | bearer | The `Stop` hook, and the next prompt's hook: a session's turn ended or started |
| `GET /v1/sessions/{session}` | bearer | Looking up a session's draft without bumping it (`task` without a name) |

### `GET /health`

`200 {"status": "ok"}`, or `503 unavailable` when the database is unreachable.

### `POST /v1/register`

Creates an owner for an auth token. With `import`, it also uploads a whole local setup **in the same transaction**:
if anything in it fails, nothing is created (not even the owner), and the same request can be retried. This is the
single request `rostr cloud on` makes for a new setup; `assemble --cloud` sends its one roster the same way.
A second machine with an existing key doesn't register: it calls `GET /v1/settings`, and `401` means the key is
unknown.

Rate-limited per client IP.

```json
{
  "authToken": "<base64url, 43 chars>",
  "import": {
    "rosters": [
      {
        "name": "work",
        "emojiMode": "on",
        "aTeam": [{ "name": "Gandalf", "emoji": "🧙" }],
        "bTeam": [],
        "createdAt": "2026-01-01T00:00:00Z"
      }
    ],
    "settings": { "defaultRoster": "work", "draftExpiry": "7d" },
    "links": [{ "repoHash": "3f2a…", "repo": "🔒", "roster": "work" }],
    "drafts": [
      {
        "roster": "work",
        "name": "Gandalf",
        "emoji": "🧙",
        "copy": 1,
        "session": "5f0c…",
        "repoHash": "3f2a…",
        "repo": "🔒",
        "task": "🔒",
        "draftedAt": "2026-10-20T09:00:00Z",
        "lastSeenAt": "2026-10-20T09:30:00Z"
      }
    ]
  }
}
```

- `import` and every list in it are optional. In rosters, `emojiMode`, `bTeam`, and `createdAt` are optional.
  `createdAt` keeps the local order, which decides the "oldest roster" that `disband` promotes; without it, rosters
  are dated in the order they're listed.
- `settings` fields are optional. Without `defaultRoster`, the oldest roster becomes the default.
- Links and drafts name a roster from the same import.
- Drafts are the **active** drafts only. `copy` defaults to 1; `repoHash`/`repo` come together or not at all;
  `task`, `draftedAt`, and `lastSeenAt` (default: now) are optional. A draft's member needn't be in the current cast.

**`201`** with the Settings object. Errors: `400 invalid_request`, `409 already_registered`, `409 roster_exists`,
`409 conflict` (for example, two drafts of the same member and copy), `429 rate_limited`.

### `GET /v1/stats` (public, stable)

Every roster with its cast, drafted agents, and idle members, in the shape of `rostr stats --json`. It's everything
`rostr stats` shows, in one call.

```json
{
  "rosters": [
    {
      "name": "work",
      "emojiMode": "on",
      "aTeam": [{ "name": "Gandalf", "emoji": "🧙" }, { "name": "Frodo", "emoji": "💍" }],
      "bTeam": [],
      "isDefault": true,
      "createdAt": "2026-10-20T09:57:14Z",
      "drafted": [
        {
          "name": "Gandalf 2",
          "emoji": "🧙",
          "team": "a",
          "task": "🔒 base64 | null",
          "repo": "🔒 base64 | null",
          "draftedAt": "2026-10-20T09:57:14Z",
          "restingSince": "2026-10-20T10:31:02Z | null"
        }
      ],
      "idle": [{ "name": "Frodo", "emoji": "💍", "team": "a" }]
    }
  ]
}
```

- `aTeam`, `bTeam`, `isDefault`, and `createdAt` are as in the [Roster](#objects) object. `isDefault` is whether it's
  the owner's default roster.
- Rosters are oldest first. `drafted` is oldest draft first; `idle` is in draft order: the order the next drafts
  take them (A team first, least recently drafted first).
- In `drafted`, `name` includes the copy number (`Gandalf 2`), `emoji` is the one stored on the draft, and `team` is
  `null` if the member has left the cast.
- `idle` is the cast members whose name (copy 1) is free, which is who the next draft can pick. So with `Gandalf 2`
  drafted and copy 1 free, Gandalf is listed as idle too.
- `restingSince` is when the agent's session finished its last turn, once it has been resting: the turn ended at
  least 15 minutes ago and no new one has started. It's `null` while the agent is working or rested less than that.
  A draft takes a resting agent's name only when no name is free (see `POST /v1/agents/draft`).
- A draft not seen within its owner's `draftExpiry` is benched by the server's background sweep, which runs every
  15 minutes, so it can still be listed for up to 15 minutes past its expiry.
- `drafted` and `idle` are `[]` when empty, never `null`.
- `task` and `repo` are ciphertext; everything else is plaintext, so a "who's busy" widget needs no key.

### `POST /v1/rosters`

```json
{ "name": "work", "emojiMode": "on", "aTeam": [...], "bTeam": [...] }
```

**`201`** with the Roster. The owner's first roster becomes the default. Errors: `400`, `409 roster_exists`.

### `GET /v1/rosters/{name}`

**`200`** with the Roster. Errors: `404 roster_not_found`.

### `PUT /v1/rosters/{name}`

Saves `rostr edit` (and `config emoji`, which sends the whole roster back with a new mode). The name can't change.

```json
{ "emojiMode": "on", "aTeam": [...], "bTeam": [...] }
```

Every actively drafted member must still be in the cast with exactly the same name; their emoji and team can
change. **`200`** with the Roster. Errors: `400`, `404 roster_not_found`, `409 member_drafted` (the message names
them).

### `PUT /v1/rosters/{name}/cast`

Replaces the cast (`assemble`, "replace an existing roster's cast"). Same body as `PUT /v1/rosters/{name}`, without
the drafted-member check. The roster keeps its name, project links, and default status; drafted agents keep their
names and stored emoji until their sessions end; new sessions draft from the new cast. **`200`** with the Roster.

### `DELETE /v1/rosters/{name}`

Disbands a roster, deleting its drafts and project links. If it was the default, the oldest remaining roster becomes
the default. If it was the owner's **last** roster, the owner is deleted too (settings and all), so nothing of theirs
is left; the auth token gets `401 unauthorized` from then on, and registering it again starts a fresh, empty owner.

```json
{ "name": "work", "benchedDrafts": 2, "unlinkedProjects": 1, "newDefault": "home", "ownerDeleted": false }
```

`newDefault` is set only when the default changed; it's `null` when the roster wasn't the default, or was the last
one. `ownerDeleted` is `true` when this was the last roster; the CLI then removes its `credentials` and goes back to
local mode. **`200`**. Errors: `404 roster_not_found`.

### `GET /v1/settings`

**`200`** with Settings.

### `PATCH /v1/settings`

Any subset of the Settings fields; the rest are unchanged. `defaultRoster` names an existing roster.

```json
{ "draftExpiry": "48h" }
```

**`200`** with the updated Settings. Errors: `400`, `404 roster_not_found`.

### `GET /v1/links`

**`200`** `{"links": [Link, ...]}`, ordered by roster name.

### `GET /v1/links/{repoHash}`

**`200`** with the Link. Errors: `400` (bad hash), `404 link_not_found`; the project then uses the default roster
from `GET /v1/settings`.

### `PUT /v1/links/{repoHash}`

Links a project, replacing any existing link.

```json
{ "repo": "🔒", "roster": "work" }
```

**`200`** with the Link. Errors: `400`, `404 roster_not_found`.

### `DELETE /v1/links/{repoHash}`

**`204`**. Errors: `404 link_not_found`.

### `POST /v1/agents/draft`

```json
{ "session": "5f0c…", "roster": "work", "repoHash": "3f2a…", "repo": "🔒", "task": "🔒", "idle": true }
```

Only `session` (1 to 256 bytes) is required. `roster` is the `--roster` flag. `repoHash` and `repo` (the current
project) come together or not at all. `task` is the placeholder or `--task`. `idle` means the session hasn't had a turn
yet: the CLI drafts a session when it starts, ahead of its first prompt. An idle draft counts as a turn that ended at
the draft, so a session opened and never prompted rests after 15 minutes (see `POST /v1/agents/turn`) and its name can
be taken.

A draft reads what it needs in one round trip to the database, picks the member, and writes the draft in a second. It
takes no database lock: the unique indexes on active drafts (one per name and copy, one per session) stop two drafts
from taking the same name or session, and a draft whose write loses that race starts over. Drafts for one owner on one
server instance take turns.

1. **Same session:** if the session has an active draft in any of the owner's rosters, bump its last-seen time, set
   `task` if one was sent, and return it.
2. **Resume:** if the session had a draft before, its member is still in that roster's cast, and that member and
   copy are free, draft them again in the same roster, with the new `repo` and `task` if sent, or the old ones if not.
3. **Otherwise**, pick the roster: `roster` if sent, else the project's link for `repoHash`, else the default.
   - Draft the free member drafted least recently, A team first.
   - If all are busy, take the name (copy 1) of the member who has rested longest: their draft is benched and the new
     session gets the member, so the resting session loses the name (see `POST /v1/agents/turn`).
   - With no one resting, a free copy 2, then 3, and so on.

**`200`** with the Draft. Errors: `400`, `401`, `404 roster_not_found`, `404 no_rosters`.

### `POST /v1/agents/keepalive`

```json
{ "session": "5f0c…" }
```

Steps 1 and 2 of drafting, never step 3: it bumps the session's active draft, or, if it expired, reclaims the same
member when they're free. It never gives the session a different member, so the hourly prompt-hook keep-alive and the
compaction re-tell use it. **`200`** with the Draft, or **`404 not_drafted`**, meaning the session stays unnamed.

### `POST /v1/agents/bench`

By session (the `SessionEnd` hook, `bench` without a name):

```json
{ "session": "5f0c…" }
```

Or by name, across all the owner's rosters, optionally narrowed with `roster` (`bench <name>... [--roster]`, once per name):

```json
{ "name": "gandalf 2", "roster": "work" }
```

Send exactly one of `session` and `name`. A name matches the drafted display name (`Gandalf`, or `Gandalf 2` for a
copy), ignoring case and accents. **`200`** with the benched Draft. Errors: `400`, `404 not_drafted` (nothing to
bench, for example after `disband`; the CLI treats it as done), `404 roster_not_found`, `409 ambiguous_name` (with
`rosters`).

### `POST /v1/agents/task`

The same target as bench (`session`, or `name` with optional `roster`), plus the required task:

```json
{ "session": "5f0c…", "task": "🔒" }
```

Sets the task and bumps last-seen. **`200`** with the Draft. Errors: as for bench.

### `POST /v1/agents/turn`

```json
{ "session": "5f0c…", "ended": true, "at": "2026-10-20T10:31:02Z" }
```

Records that the session's turn ended (`ended: true`, from the `Stop` hook) or started (`false`, from the next
prompt's hook) at `at`, the client's time, never later than now, and bumps last-seen. Each time only moves forward,
so a report that arrives late can't undo a newer one. A draft whose turn ended at least 15 minutes ago, with no turn
started since, is resting (`restingSince` in `GET /v1/stats`). **`204`**, or **`404 not_drafted`** when the session
has no active draft, as when another session took its name while it rested.

### `GET /v1/sessions/{session}`

The session's active draft, without bumping it. **`200`** with the Draft, or **`404 not_drafted`**.
