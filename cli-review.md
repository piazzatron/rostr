# CLI review findings (temporary)

From the subagent's review of the CLI design. Delete this file once each item is decided.

**Done:** #2 (how commands know their session). Hooks get `session_id` in their JSON input at start and end, and
pass `--session` themselves. The start hook writes `ROSTR_SESSION` to `CLAUDE_ENV_FILE`, so commands the agent runs
pick it up automatically; the name is an optional fallback. The first prompt sets the initial task. Codex support
needs checking (in todo.md).

**Done:** #1 (which roster to draft from, and where it lives). Fixed by making a whole setup either local or cloud,
plus the lookup order `--roster` → project link → default, with projects identified by their `origin` remote.

## Should fix before building

### 3. Releasing mid-session conflicts with the name staying in the title

- **Where:** README "How it works" step 3, and the `release` summary ("the skill has your agent do this when its work
  is done").
- **Problem:** if the agent releases and you keep talking to it, the title still says Gandalf while a new session can
  draft Gandalf. Two Gandalfs.
- **Suggested fix (v1):** only the session-end hook and the 48h expiry release. The skill releases only when you ask.
  Update README step 3 to match.

### 4. `draft` without `--session` is undefined

- **Where:** `draft` on the docs page. It's also shown to people in the README and on the home page.
- **Problem:** without a session ID there's nothing to be idempotent on, the hooks never release it, and the server's
  `session_id` has no value.
- **Suggested fix:** require `--session` (or the env var from #2) and present `draft` as a hooks command. Or generate
  an ID and print it, but requiring is simpler.
  - Also make `rostr release` with no name and no session an error, not "release whatever's mine".

### 5. A long-running session can lose its name while still active

- **Where:** both storage docs, drafting step "expire stale drafts".
- **Problem:** `last_seen_at` is only bumped by re-drafts and `assign`. A session open for more than 48h without an
  `assign` gets expired and its name re-drafted while it's still working.
- **Suggested fix:** the hook runs `rostr draft --session X` on every prompt (idempotent, so it doubles as a
  keep-alive). The existing hook already runs on `UserPromptSubmit`.
  - **Note:** this is the per-prompt refresh we dropped earlier. Free locally, but one API call per prompt in cloud
    mode. A simpler option is a much longer expiry, say 7 days.

### 6. Hook failure behavior is unspecified

- **Where:** `draft` and `release`.
- **Problem:** nothing says what happens on API down, no key, no roster, or every member busy. A hook that errors or
  hangs blocks the session.
- **Suggested fix:** specify the hook contract.
  - Short network timeout.
  - Always exit 0 when called with `--session`, printing nothing (or `{"name": null, "reason": …}` with `--json`).
  - All members busy: either no name, or a fallback like "Gandalf 2". Pick one.

### 7. The A team is defined twice

- **Where:** both storage docs: array order ("the first six are the A team") plus a per-member `team` field.
- **Problem:** if order and `team` disagree, "first free member in roster order" can draft a B-teamer before an
  A-teamer.
- **Suggested fix:** store separate `aTeam: [...]` and `bTeam: [...]` arrays and drop the `team` field. Easier to edit
  too.

### 8. Name matching and filenames are unspecified

- **Where:** the example `assign samwise` against member "Samwise", the file `drafts/<roster>/gandalf.json`, and
  preset names like "Donkey Kong", "R2-D2", "Éowyn", "Captain Falcon".
- **Suggested fix:** match member and roster names ignoring case and accents. Use a simplified name (slug) for
  filenames. `edit` rejects names that collide once simplified.
  - Also define what renaming or removing a drafted member in `edit` does. Simplest: refuse.

### 9. Resume breaks locally when a session dies

- **Where:** local-storage.md, "Releasing … writes `sessions/<session-id>.json`".
- **Problem:** a session that dies and expires never writes that file, so it can't resume, while the server version
  can. Retention also differs: local deletes after 7 days, the server keeps rows forever.
- **Suggested fix:** write the session file at draft time. Same retention on both: prune released server rows older
  than 7 days during the expiry step.

### 10. No way to use an existing key on a second machine

- **Where:** `key`.
- **Problem:** `rostr key` prints the key, but nothing saves it on another machine.
- **Status:** partly covered now that `ROSTR_KEY` is documented: setting it on a second laptop puts that laptop in
  cloud mode. A `rostr key set <key>` that saves it to `credentials` would still be nicer than an env var.

### 11. The three command lists disagree

- `setup` and `key` are on the docs page but missing from the home page list and the README table. `setup` does
  appear in the README install steps.
- `disband`: README shows no argument and says "Delete your roster"; the docs page requires `<roster>`.
- `release <name>`: required in the README and home page, optional on the docs page.
- **Suggested fix:** generate the home page list from the docs page data (a summary plus a "featured" flag) so they
  can't drift. Make the README table a short list that links to /cli.

### 12. Roster and flag arguments are inconsistent

- `<roster>` is required for `use` and `disband`, optional for `list` and `edit`, and missing from `draft`, `assign`,
  and `release`.
  - **Suggested fix:** positional `<roster>` only for `create` and `disband` (explicit, because destructive).
    Everything else uses the global `--roster` flag, defaulting to the lookup order.
- `--json` is only on `draft` and `list`.
  - **Suggested fix:** make it a global flag.
- **Done:** the `create --local | --cloud` vs `transfer --to` mismatch went away with the local-or-cloud change.

## Nice to have

### 13. Cut for v1

- `draft --task`: duplicates `assign`, and the task usually isn't known at session start.
- `use --repo`: just `cd` into the repo.
- `presets`: print presets when `create` runs without `--preset` or `--names`, and let `rostr create fellowship` use
  the preset of that name. (Bare `create` is now defined: it picks a random preset and names the roster after it.)

### 14. Show the current roster and allow unlinking

- The only way to see which roster a project uses is the header of `rostr list`, and there's no way to remove a link.
- **Suggested fix:** bare `rostr use` prints the roster and why ("work (linked)" or "work (default)"). Add
  `rostr use --unlink`.
- `disband` on the default roster leaves no fallback. Require choosing a new default first, or refuse.

### 15. Example and output nits

- After `rostr create work --preset fellowship`, the `list` example header says "fellowship · …". It should be the
  roster name, `work`.
- The docs page `list` example says "4 of 6 drafted" but shows 2 rows, and lists Legolas and Gimli as idle.
- `draft --json` should return a ready-to-use display string (emoji included or dropped per the roster's setting) so
  hooks don't re-implement it.
- Rosters made with `--names` get no emoji. Say so, or assign a default.
- `key` and `presets` are nouns, not verbs. Fine, but `key` could become `key show` / `key set` / `key rotate` once
  `set` exists (#10).
