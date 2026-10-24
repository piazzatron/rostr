# CLI review findings (temporary)

Second review, against the current CLI (`assemble`, `link`/`unlink`, `agent draft|release|task`, `config …`).
Write each decision into the code (and [`docs/`](docs/README.md) if it changes how a system works), then remove it here. Delete this file once
each item is decided.

**Done:**

- old #1 (which roster to draft from).
- old #2 (how commands know their session).
- old #11 (the three command lists now agree).
- old #3 (names are released only at session end or expiry; `/release` is cut).
- old #4 (agent commands error without a session).
- old #5 (7-day expiry; release stuck names by hand).
- N1 (repo names stored encrypted alongside the fingerprint).
- N2 and old #10 (the CLI generates the key; `config key` prints it; `config cloud on` takes an existing key at a
  hidden prompt).
- old #6 (hooks always exit 0, skip without rostr installed; everyone busy drafts "Gandalf 2").
- old #7 (separate `aTeam` / `bTeam` lists).
- old #8 (case- and accent-insensitive names, slug filenames, `edit` refuses changes to drafted members).
- old #12 (arguments vs. `--roster`).
- old #15 (typed emoji are kept; the rest are assigned at random).
- old #13 (keep `--repo`, `--task`, and `presets`).
- N3 (a session's roster is fixed at its first draft; later steps find it by session ID).
- N5 (cloud is one-way; the upload is one request; `disband` deletes cloud data).
- N7 (running `assemble` again makes another roster, after asking if you meant `edit`).
- N4 (the local expiry race is rare enough to accept).
- N12 (one public read endpoint; tasks and repos stay encrypted; decryption is a recipe, not a package).
- N9 (cast swaps moved from `edit` to `assemble`; drafts store their emoji).
- old #9 (released drafts kept 7 days on both sides; local storage is now SQLite).
- N8 (projects are `owner/repo` from `origin`, normalized; no origin uses the main folder name).
- old #14 (bare `rostr link` shows and changes the project's roster; `disband` asks first, releases drafts, and
  promotes another roster to default).
- N6 (auto-draft is stored per setup; headless `claude -p` runs aren't drafted).

## New findings

### Should fix before building

### Nice to have

#### N10. `emoji only` has holes; consider cutting it for v1

- Unique-emoji check timing (edit, cast swap, kept drafts), `--names` rosters, variation selectors and ZWJ sequences,
  "🧙" alone as a sign-off. `config emoji` is per roster but `rostr config` shows one global value.
- **Fix:** cut `only` for v1, or validate on every write. Show emoji mode per roster.

#### N11. Copy contradicts itself

- Home page: "Every session shares one roster" (people have many; say "one setup"). "Task and repo metadata are
  end-to-end encrypted" (repos are fingerprinted, unless N1 lands).
- /privacy "What the server sees" omits IP addresses and roster names.
- /cli examples: the default is `smash-bros` in `unlink` but `work` elsewhere; `list` uses `rostr/api` while the rest
  uses `piazzatron/rostr`; "cloud" in the where column is undefined.
