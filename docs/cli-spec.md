# rostr CLI spec

The source of truth for how the `rostr` CLI, its hooks, and its skill behave. Build from this file. The site's
/cli page and the README are user-facing docs written from it, and storage details live in
[local-storage.md](local-storage.md) and [server-schema.md](server-schema.md).

Items marked **Open** are undecided; the number points to the finding in [`cli-review.md`](../cli-review.md).

## Overview

rostr gives every coding-agent session (Claude Code, Codex) a name drafted from a roster, like 🧙 Gandalf from The
Fellowship. The name goes at the front of the session title (`🧙 Gandalf · Fix auth redirect loop`) and is freed
when the session ends.

- **Platforms:** one Go binary for macOS and Linux. No Windows in v1.
- **Pieces:** the CLI, hooks that call it at session start and end, a skill that teaches the agent to use it, and the
  `/draft` slash command.

## Concepts

### Rosters

A roster is a named list of members, each a name and an emoji. A person can have many rosters.

- **A team and B team.** Each roster has an A team (six by default) and a B team, stored as two separate lists.
  Drafting picks from the A team first, whoever was drafted longest ago, and only calls up the B team when every
  A-teamer is busy.
- **Presets.** Pre-built rosters ship with the CLI. The list lives in `site/src/data/rosters.ts` (id, name, source,
  emoji, A team, B team). Free Agents is a new random draw from the other presets each time it's
  used, the same size as the listed one, with no two members sharing a name or emoji.
- **Names match loosely.** Member and roster names match ignoring case and accents, so `rostr bench eowyn`
  finds Éowyn. A roster can't have two members whose names match this way, and you can't have two rosters whose
  names do.

### Local or cloud

A whole setup is either local or cloud, never mixed.

- **Local** (the default): everything lives in a SQLite database at `~/.config/rostr/rostr.db`. SQLite is compiled
  into the CLI, so there's nothing to install. No network calls.
- **Cloud:** everything lives on the rostr API at `api.getrostr.sh`, so sessions on any machine or in cloud
  environments (Claude Code on the web, Codex cloud) draft from the same rosters.
- A machine is in cloud mode whenever it has a rostr key, from `~/.config/rostr/credentials` or the `ROSTR_KEY`
  environment variable.

### Projects

Each git repo can link to its own roster. A project is identified by its `origin` remote as `owner/repo`, which is
the same on every machine, clone, and worktree (folder names aren't).

- **Normalizing:** the last two segments of `origin`'s URL path, lowercased, with `.git` dropped. So
  `git@github.com:Piazzatron/rostr.git`, `https://github.com/piazzatron/rostr`, and SSH host aliases all give
  `piazzatron/rostr`.
- **No `origin`:** the project is the repo's main folder name (the same for all its worktrees).
- **Outside git:** there's no project, so the default roster is used, and `rostr link` errors.
- **Forks** are their own projects, since they have their own `origin`.
- **Verify:** that cloud sessions (Claude Code on the web, Codex cloud) have an `origin` ending in `owner/repo`, even
  when they clone through a proxy.

### Which roster gets drafted

1. The `--roster` flag, if passed.
2. The current project's linked roster.
3. The default roster.

Outside a git repo, rostr uses the default.

- **Arguments vs. `--roster`.** Commands that act on a roster take it as an argument: `assemble`, `link`, `list`,
  `edit`, `disband`, `config default`. Commands that pick whose agents to act on take `--roster`, which overrides
  the lookup above: `draft`, `bench`, `task`, `config emoji`.
- **A session's roster is fixed at its first draft,** from the directory the session started in. After that,
  everything for the session (resume, compaction, keep-alive, `task`, `bench`, session end) finds its
  draft by session ID, so an agent that `cd`s into another project keeps its name.

### Sessions

Every draft belongs to a session ID from Claude Code or Codex.

- Hooks pass the session ID with `--session`.
- The Claude Code start hook writes `export ROSTR_SESSION=<id>` to `CLAUDE_ENV_FILE`, so rostr commands the agent
  runs know their session without flags. Codex hooks can't set env vars, but Codex gives every command its agent runs
  `CODEX_THREAD_ID`, its hooks' session ID. `--session` defaults to `ROSTR_SESSION`, then `CODEX_THREAD_ID`.
- Drafting always happens inside a session (a hook, `/draft`, or `$rostr-draft`); nobody drafts from a bare terminal.
  With no `--session`, `ROSTR_SESSION`, or `CODEX_THREAD_ID`, `draft` errors, and so do `bench` and
  `task` without a name.

### Display

How an agent shows up depends on the roster's emoji mode:

| Mode | Display |
|---|---|
| `on` (default) | `🧙 Gandalf` |
| `off` | `Gandalf` |
| `only` | `🧙` (needs every member to have a different emoji) |

The session title is `<display> · <task>`.

- **Open (N10):** cut `only` for v1, or define when uniqueness is checked.
- **Emoji for your own names.** With `--names`, a name can come with its emoji (`"🧮 Ada"`). Names without one get a
  random emoji, different from every other member's, so `only` mode works for every roster.

## Commands

### Roster commands

With no rosters yet, every command that needs one says: "No rosters yet. Run `rostr assemble`."

#### `rostr assemble`

```
rostr assemble [<roster>] [--preset <id> | --names <list>] [--emoji on|off|only] [--cloud] [--no-auto-draft] [--link]
```

Interactive setup. The wizard runs full-screen: how to build the roster (a pre-built roster, with a preview of its
cast; Free Agents, a random mix from every roster; or your own names), then the cast editor, then one last page that shows the cast and confirms every setting with its default: roster name, emoji, and,
on the first run, where rostr lives (the cloud by default, or this machine) and whether to draft every session
automatically. Nothing is saved until the last page is done; shift+tab goes back, and esc cancels. Each question can be
skipped with a flag, so an agent can run it non-interactively. Making a cloud setup at a terminal ends by printing the
new rostr key and how to use it on other machines and in cloud environments.

- `--preset <id>`: use a pre-built roster.
- `--names <list>`: comma-separated names, each optionally with an emoji (`"🧮 Ada, Grace"`). The first six are
  the A team. Names without an emoji get a random one.
- `--emoji on|off|only`: the roster's emoji mode. Defaults to `on`.
- `--cloud`: put the setup in the cloud. Only for the first roster; after that, use `rostr cloud on`.
- `--no-auto-draft`: turn auto-draft off.
- `--link`: use the new roster for the repo you're in. Run in a git repo with a roster already set up, the wizard
  asks this itself ("Use the side roster for acme/api?"); the first roster is the default, so it's never linked.
- The first run also installs the hooks, the skill, and the `/draft` slash command for Claude Code, and, where Codex
  looks installed (a `~/.codex` folder or `codex` on PATH), the Codex hooks and skills (see [Codex](#codex)). It
  tells Codex users to approve the hooks in `/hooks`.
- `create` is a hidden alias, so people who guess it still get a roster.
- **Running it again.** If you already have a roster, the wizard first asks what you want: make a new roster,
  replace an existing roster's cast, or edit one (which opens `rostr edit`). Agents passing flags always make a new
  roster.
- **Replacing a cast** keeps the roster's name, project links, and default status. Agents already drafted keep their
  names until their sessions end (each draft stores its member's emoji, so `rostr stats` still shows them); new
  sessions draft from the new cast.
- Later runs skip the setup-wide questions (where rostr lives, auto-draft); change those with `rostr config`. They
  reinstall missing hooks without duplicating them, and replace an older version's skill and `/draft` text (see
  [Upgrading](#the-rostr-hook-command)).
- A name that's already taken asks for another one (or errors, when run with flags). `--cloud` on a later run errors
  and points to `rostr cloud on`.

#### `rostr link` / `rostr unlink`

```
rostr link [<roster>] [--repo <owner/repo>]
rostr unlink [--repo <owner/repo>]
```

Link a roster to the current project, or unlink it to go back to the default. `--repo` targets another repo.

- **Bare `rostr link`** is interactive: it shows which roster the project uses now and why (linked, or your default),
  and lets you pick another. Run by an agent (no terminal), it just prints the current roster and why.

#### `rostr stats`

```
rostr stats [<roster>, ...] [--json]
```

Shows every roster, headed `<name> roster`, with its working agents (display, task, repo, time since drafted), then
under `Resting:` the agents whose session finished its turn at least 15 minutes ago (with the time they've rested; see
[Resting](#resting)), then its members on the bench. Name one or more rosters, separated by commas, to show just those. `--json` prints the
same data as `{"rosters": [{"name", "emojiMode", "drafted", "resting", "bench"}]}`. `rostr list` still works as an alias.


#### `rostr edit`

```
rostr edit [<roster>]
```

Opens the roster in the cast editor (see below) to add, rename, and remove members, change their emoji, and move them
between the A and B teams, then a last page for the roster's emoji mode and a Save button. Terminal only. To swap in a
whole new cast, run `rostr assemble` and choose to replace the roster's cast.

- **The cast editor** is one list, the A team then the B team. ↑/↓ moves, space switches the highlighted member's
  team, `a` adds (one name, or several separated by commas, each with an optional emoji in front; new members fill the
  A team to six, then join the B team), `r` renames (and changes the emoji, if one is typed), `x` removes, and enter
  finishes. Names without an emoji get one no one else on the roster has. It starts adding straight away for an empty
  cast. `rostr assemble` uses the same editor for every new cast, prefilled from the preset or Free Agents.

- Refuses to save an edit that renames or removes a drafted member. Wait for their session to end, or release them
  first.

#### `rostr presets`

Lists the pre-built rosters: id, emoji, name, source.

#### `rostr disband`

```
rostr disband <roster> [--yes]
```

Deletes a roster. Projects linked to it fall back to the default. In cloud mode this deletes it from the server too,
with its drafts and project links.

- **Asks first:** "Disband work? 2 agents drafted, 1 linked project." Agents (no terminal) must pass `--yes`.
- **Active drafts are released.** Those sessions keep the name in their title until they end; their later
  keep-alives and releases find nothing and do nothing.
- **Disbanding the default** makes your oldest remaining roster the default, and says so. Disbanding your last
  roster puts you back where you started, as if you'd never run `assemble`.
- **In cloud mode, disbanding your last roster deletes your whole cloud setup,** so nothing of yours is left on the
  server, and the rostr key stops working. This machine goes back to local mode (its `credentials` file is removed;
  if `ROSTR_KEY` is set, it says to unset it). Other machines and cloud environments still holding the key get "key
  not recognized" errors until it's removed, and their hooks just leave sessions unnamed. Starting again
  (`rostr assemble --cloud`) makes a new key.

#### `rostr uninstall`

```
rostr uninstall [--yes]
```

Undoes `assemble` completely: deletes every roster (in cloud mode that deletes the whole cloud setup, and the key
stops working), removes this machine's key or local database, and removes the hooks, skill, and `/draft` from Claude
Code (only rostr's own entries, and a `/draft` the user wrote themselves is left alone). Asks first; agents pass
`--yes`.

### Agent commands

These are run by the hooks, the skill, and `/draft`, not usually by people.

#### `rostr draft` / `rostr bench`

```
rostr draft [--task <description>] [--session <id>] [--roster <roster>] [--json]
rostr bench [<name>, ... | all] [--session <id>] [--roster <roster>]
```

- **Draft** gives the session the next free member, A team first (see [Drafting](#drafting)). Drafting is
  idempotent per session: drafting again returns the same member.
- `--json` prints `{"name", "emoji", "team", "roster", "display"}`, where `display` follows the emoji mode, so hooks
  don't rebuild it.
- **Bench** frees the session's member, the named members (separated by commas, since names can have spaces), or with `all` every drafted member (only `--roster`'s, if
  given). Benching by name is the manual fix for names left stuck by sessions that died, so it searches all your
  rosters, not just the current one. If more than one roster has a member drafted, it asks which (or takes
  `--roster`). Every name is looked up before any is benched, so a typo benches nobody.

#### `rostr task`

```
rostr task [<name>] <description> [--session <id>] [--roster <roster>]
```

Sets what an agent is working on. Without a name, it sets the task for the session's agent. Bumps the draft's
last-seen time.

### Settings

#### `rostr config`

```
rostr config [--json]
rostr config default <roster>
rostr config emoji on|off|only [--roster <roster>]
rostr config auto-draft on|off
rostr config expiry <duration>
rostr config key [--token]
```

On its own, prints every setting.

- `default`: the roster projects use when they don't have a link.
- `emoji`: the emoji mode of the current roster (per the lookup order).
- `auto-draft`: draft every new session automatically, or only with `/draft`. Off means sessions start unnamed.
- `expiry`: how long a draft can go unseen before it's released (see [Drafting](#drafting)). Takes hours, days, or
  weeks (`48h`, `7d`, `2w`). Defaults to `7d`. Applies to all your rosters.
- `key`: prints the rostr key, so you can paste it on another machine or into a cloud environment's variables as
  `ROSTR_KEY`. Refuses when it isn't running in a terminal (which is how agents run commands), so the key doesn't
  land in an agent's transcript by accident.
  `--token` prints the derived auth token instead, for calling the [public API](#public-read-api) with `curl`.
- `expiry` is stored with your setup (the local settings table, or your account in cloud mode), so it applies on
  every machine. `auto-draft` is a setting of each machine: in local mode it's in the settings table, and in cloud
  mode in `~/.config/rostr/auto-draft`, so a session start checks it without a call. `ROSTR_AUTO_DRAFT=on` or `off`
  overrides it, which is how a cloud environment, whose containers start without that file, turns it off.
- **Open (N10):** emoji is per roster, but `rostr config` shows one value.


#### `rostr cloud`

```
rostr cloud on
rostr cloud install
```

- `on`: asks "New setup, or do you have a key?"
  - **New setup:** generates a key, uploads every roster, the default, project links, and active drafts to the API,
    saves the key to `credentials`, and deletes the local database. It doesn't print the key; it says where it was
    saved and points to `rostr config key`.
  - **Existing key** (a second machine): you paste the key at a prompt that doesn't echo it. Keys are never taken as
    command-line arguments, which end up in shell history and agent transcripts. Refuses if this machine already has
    local rosters. Setting `ROSTR_KEY` instead works too.
- **Cloud is one-way.** There's no `cloud off` in v1: once a setup moves to the cloud, it stays there.
- The upload is a single request, and the local database is deleted only after it succeeds, so a failed upload leaves the
  local setup untouched.
- `install`: installs the Claude Code hooks in a Claude Code cloud session's container (`CLAUDE_CODE_REMOTE=true`),
  where each session starts from a fresh container. The install script runs it there, and `ROSTR_KEY` connects the
  container to the setup. Anywhere else it refuses and points to `rostr assemble`, which installs the hooks on a
  person's own machine.
## Hooks, skill, and slash command

### The `rostr hook` command

Installed hooks are one-liners that call `rostr hook`, which does all the work. It shows in `rostr --help`, but only
`rostr hook sync` is meant to be run by hand:

```
rostr hook claude-code session-start
rostr hook claude-code prompt
rostr hook claude-code title-set
rostr hook claude-code turn-end
rostr hook claude-code session-end
rostr hook claude-code draft          # run by /draft
rostr hook codex session-start
rostr hook codex prompt
rostr hook codex turn-end
rostr hook codex session-end
rostr hook codex draft                # run by the $rostr-draft skill
rostr hook sync                       # reinstalls the hooks after an upgrade (see Upgrading)
```

Each reads the hook's JSON input from stdin, does the work, and prints the JSON reply the agent expects. The logic,
and the exact text the agent is told, live in the CLI, so upgrading rostr changes them without reinstalling hooks.

**Upgrading.** Only a change to what rostr installs (a new hook event or matcher, a new permission, new skill or
`/draft` text) needs anything rewritten. Each install records a fingerprint of what it wrote in
`~/.cache/rostr/hooks/<agent>`. Every session start (Claude Code and Codex) compares the stamps with the binary's own
fingerprints, which costs a file read, and when one differs it starts `rostr hook sync` in the background. That
reinstalls for each agent that has rostr's hooks already, never adding them where there are none, and updates the
stamps. Installing replaces an older version's skill text, and `/draft` when it's rostr's (it runs `rostr hook
claude-code draft`), so a `/draft` of your own is left alone. Claude Code picks up the rewritten settings on its own, so
after `brew upgrade` the first session that starts brings everything up to date. The install script runs `rostr hook
sync` too. New Codex hooks still need approving in Codex's `/hooks`.

### Claude Code events

| Event | What `rostr hook` does |
|---|---|
| `SessionStart`, source `startup` or `clear` | Writes `export ROSTR_SESSION=<id>` to `CLAUDE_ENV_FILE`. In cloud mode (when auto-draft is on, and not for a headless `claude -p` run), drafts the session in the background, so its name is waiting by its first prompt (see [Drafting ahead](#drafting-ahead)) |
| `SessionStart`, source `resume` | Writes `ROSTR_SESSION`, drafts (asking for the session's old name back, see [Drafting](#drafting)), and tells the agent who it is |
| `SessionStart`, source `compact` | Re-tells the agent who it is, without drafting a new member (see below) |
| `UserPromptSubmit`, first prompt | The draft step, below (when auto-draft is on). In cloud mode it uses the draft made when the session started, if it's there, and drafts otherwise |
| `UserPromptSubmit`, every prompt | Records that the turn started, renaming the session if it lost its name while resting (see [Resting](#resting)); keeps the draft alive, at most once an hour (see [Keep-alive](#keep-alive)) |
| `PostToolUse`, any tool | Records that the turn started, if nothing has since the last turn ended: a turn can start without a prompt, when another `Stop` hook continues it or a background task wakes the agent (see [Resting](#resting)) |
| `PostToolUse`, a title tool or `Bash` | Records the summary in a `<name> · <summary>` title as the agent's task, and ends a turn that was a plain answer (below), recording that the turn ended, since the `Stop` hook may not run after that |
| `Stop` | Records that the turn ended (see [Resting](#resting)) |
| `SessionEnd` | Releases the session's member |

- Release uses `SessionEnd`, not `Stop` (`Stop` fires after every reply).
- Names are benched at session end, by the expiry backstop, or when a session that has been resting loses its name
  to a new one (see [Resting](#resting)); the next prompt then renames the session and tells the agent.
- Setting the title in Claude Code: see [claude-code-session-titles.md](claude-code-session-titles.md). The prototype
  is `cli/hooks/claude-code/`.
- **Verify:** that compaction keeps the session ID, and that `/clear` starts a new one (firing `SessionEnd` for the
  old session).
- **Failures never block a session.** `rostr hook` always exits 0. If it can't draft (no roster yet, the API is
  unreachable or slow, a bad key), the session starts unnamed and the agent is told nothing. Network calls time out
  after 3 seconds.
- **Without rostr installed, hooks do nothing.** Hooks committed to a repo's `.claude/settings.json` also run for
  teammates who don't have rostr, so each installed command checks first:
  `if command -v rostr >/dev/null; then rostr hook claude-code session-start; fi` (not `… || true`, which would
  swallow a hook's exit code).
- With auto-draft off, only the draft step is skipped: `ROSTR_SESSION` is still written and `SessionEnd` still
  releases, so `/draft` works.
- **Headless runs aren't drafted.** Scripts and tools that run `claude -p` fire the same hooks, but don't get a name.
  Neither the hook input nor `CLAUDE_CODE_ENTRYPOINT` tells them apart (a `claude -p` an agent starts inherits its
  session's entrypoint, and Remote Control sessions report `sdk-cli` too), so the hooks read how the Claude process
  that ran them was started: `-p` or `--print` is headless, unless it's driven over stream-json (`--input-format
  stream-json`, `--sdk-url`) or has a Remote Control bridge session. Anything unreadable counts as not headless. (Claude Code 2.1.309's Remote
  Control host starts each session as `claude --print --sdk-url <url> --session-id <id> --input-format stream-json
  --output-format stream-json …`, and `--sdk-url` only works with `-p`. The hooks read the arguments with `ps -ww`,
  since a `COLUMNS` inherited from a terminal would otherwise cut them off after `--print`.)
- In tmux or herdr, the hooks also label the session's pane (see [tmux and herdr](#tmux-and-herdr)).

### Codex

Findings behind this are in [integrations-research.md](integrations-research.md#codex).

| Event | What `rostr hook codex` does |
|---|---|
| `SessionStart`, source `startup` or `clear` | Nothing (Codex already gives commands `CODEX_THREAD_ID`) |
| `SessionStart`, source `resume` | Drafts (asking for the old name back) and tells the agent who it is |
| `SessionStart`, source `fork` | A fork is a new thread carrying its parent's history and name: drafts it its own member, names the thread, and tells the agent who it is |
| `SessionStart`, source `compact` | Re-tells the agent who it is, without drafting a new member |
| `UserPromptSubmit`, first prompt | The draft step (when auto-draft is on): drafts with the start of the prompt as the task, names the thread, and adds who the agent is through `additionalContext` |
| `UserPromptSubmit`, every prompt | Records that the turn started, renaming the thread if it lost its name while resting; keeps the draft alive, at most once an hour |
| `PostToolUse` | Records that the turn started, if nothing has since the last turn ended |
| `Stop` | Records that the turn ended (see [Resting](#resting)) |
| `SessionEnd` | Releases the session's member |

- **Synchronous draft step.** Codex has no `asyncRewake`, so the first prompt waits for the draft, and the agent
  knows its name from its first reply. It isn't asked to set a title or task; the identity text tells it to run
  `rostr task` when its work changes.
- **First prompt** means the thread's rollout (`transcript_path`) has no earlier `user_message`, so threads from
  before rostr was installed, and forks, aren't drafted mid-conversation.
- **Thread names.** rostr names the thread `🧙 Gandalf · <task>` itself, through the Codex app-server: it runs
  `codex app-server proxy` (stdio to the daemon's control socket, which speaks JSON-RPC over WebSocket) and sends
  `initialize`, `initialized`, then `thread/name/set`. It gives up after 2 seconds and ignores failures; without a
  daemon (`codex --no-daemon`) threads stay unnamed. The agent's later `rostr task` updates the task, not the
  thread name.
- **Terminal title.** For tmux and herdr to show the name, set
  `tui.terminal_title = ["spinner", "thread-name", "project"]` in `~/.codex/config.toml`.
- **`SessionEnd`** is installed with `timeout: 3` (Codex's most), and the release gives up after 2 seconds.
- **Installed** to `$CODEX_HOME/hooks.json` (`~/.codex/hooks.json`), as `if command -v rostr >/dev/null; then rostr
  hook codex <event>; fi`, plus the `rostr` and `rostr-draft` skills in `~/.agents/skills` (Codex removed custom
  prompts, so `/draft` is the `$rostr-draft` skill). `rostr uninstall` removes only rostr's hook entries, and the
  skills only if unmodified.
- **Approval.** Codex runs a non-managed hook only once the user trusts it in `/hooks`. The one-line commands never
  change, so one approval survives rostr upgrades.
- **No tmux or herdr.** Codex runs hooks in a shared app-server daemon whose environment belongs to whichever
  terminal started it, so `TMUX_PANE` and `HERDR_PANE_ID` point at the wrong pane. Codex sessions rely on the
  terminal title instead.
- Failures never block a session: every `rostr hook codex` exits 0.

### tmux and herdr

Claude Code hooks only (Codex: see above). Best-effort: each command has a 1-second timeout, and failures are ignored.
The pane is labeled whenever the agent is drafted or re-told (the draft step, resume, compaction, `/draft`) and
cleared at `SessionEnd`.

- **herdr** (`HERDR_ENV=1`): `herdr pane report-metadata "$HERDR_PANE_ID" --source rostr --agent claude
  --display-agent "<display>"` replaces "claude" in herdr's sidebar; session end sends `--clear-display-agent`.
  rostr runs `$HERDR_BIN_PATH`, or `herdr` on PATH. Inside herdr, tmux is left alone.
- **tmux** (`TMUX` and `TMUX_PANE` set, outside herdr): every command targets `-t "$TMUX_PANE"`.
  - Tags the pane with the user option `@rostr` (the display), for `pane-border-format` or `status-right`
    (`#{@rostr}`).
  - Renames the window to the display only if it has one pane and `automatic-rename` is on (or rostr renamed it),
    and marks it `@rostr_renamed`.
  - Session end unsets `@rostr` and, if rostr renamed the window, turns `automatic-rename` back on.

### The draft step

Two hooks, for a session drafted on its first prompt (when auto-draft is on):

1. **`rostr hook claude-code prompt`** (`UserPromptSubmit`) runs before Claude sees the prompt. It drafts the session
   with the start of the prompt as a placeholder task, and through `additionalContext` tells the agent who it is (see
   [Identity](#identity)) and to title the session during its reply. It also sets the title to
   `🧙 Gandalf · <start of the prompt>`, so the name shows before the agent's title call, which on a long first task
   can take a minute.
2. The agent titles the session `🧙 Gandalf · <3-5 word summary>` with the session's title tool: the app's
   `mcp__ccd_session_mgmt__set_session_title` (session `"self"`, loaded with ToolSearch) in desktop Local sessions
   (`entrypoint: claude-desktop` in `~/.claude/sessions/<pid>.json`), and `mcp__claude-code-remote__set_session_title`
   (the server session ID) in Remote Control and cloud sessions. In Local sessions it makes the call after it has
   written its answer, at the end of that same message, never before the answer. In Remote Control and cloud sessions
   it makes the call first, before writing anything: those apps show the model's progress updates, and an answer
   written just before a tool call can come back as one, so the user would see a summary of the answer and then the
   answer again. Terminal sessions have no title tool, so the
   agent isn't asked: the hook starts `rostr hook summarize` in the background, which summarizes the prompt with a
   headless `claude -p --model haiku` run (the user's own login; hooks, settings, tools, and MCP servers off) and
   records the result as the task, unless the agent set one meanwhile. The next prompt's hook puts it in the title.
   If the run fails (not logged in, the plan's usage used up, anything), the title keeps the start of the prompt.
   **`rostr hook claude-code title-set`** (`PostToolUse` on both title tools and `Bash`) records the summary as the
   task. When Claude has written text and every tool call since that text was titling (any ToolSearch, a title
   tool, `rostr task`), the answer is finished, so the hook returns `continue: false` and the turn ends there:
   otherwise Claude would have to reply to the tool's result, and Claude Code doesn't let a turn end without visible
   text, so it would repeat its answer. When the title call came first, and always in Remote Control and cloud
   sessions, the hook tells Claude to carry on and answer the user without mentioning the rename.

On later prompts the prompt hook keeps the draft alive and retitles the session `🧙 Gandalf · <task>` whenever the task
has changed. A session that already has a name is never treated as being on its first prompt.

A hook's title reaches the desktop app for Local sessions, but not for Remote Control or cloud sessions
([anthropics/claude-code#103336](https://github.com/anthropics/claude-code/issues/103336)), which is why the agent sets
it. See [claude-code-session-titles.md](claude-code-session-titles.md) for how this design was reached.

### Compaction never changes the name

After compaction the agent has lost the context that told it its name, so the hook tells it again. That re-run
must never give the session a different member:

1. If the session has an active draft, re-tell the agent that name.
2. Otherwise (for example, its draft expired), reclaim the session's last member if it's still free.
3. Otherwise, say nothing. The session goes unnamed rather than being renamed.

It never drafts a new member, so the name in the title and the name the agent uses can't drift apart.

### Identity

Every time an agent is drafted or re-told (the first-prompt draft step, resume, compaction, `/draft`), the hook
adds this to the agent's context, with the name shown per the roster's emoji mode:

```
You're 🧙 Gandalf for this session (drafted by rostr from the "work" roster).
- When you write somewhere other than this chat, sign it "— 🧙 Gandalf": Slack messages, emails, PR descriptions,
  PR and issue comments.
- Don't put the name in code, commit messages, or files.
- Your user's own instructions about attribution or sign-offs take precedence over these.
- When your work changes, run: rostr task "<new task>"
```

- This goes in the hook output, not the skill: skills load only when a conversation looks relevant, and nothing about
  writing a PR comment looks like rostr.
- Names stay out of commits because commits outlive sessions and names get reused.
- The sign-off format is fixed in v1.

### Skill

A reference the agent loads when the user asks about rostr: who else is running (`rostr stats`), changing its task,
setting rostr up, editing rosters. Identity doesn't depend on it.

### `/draft`

For people who turn auto-draft off. Runs `rostr hook claude-code draft`, which drafts the session, tells the agent
who it is with the same text as the hooks, and sets the title. In Codex it's the `$rostr-draft` skill, which runs
`rostr hook codex draft` and names the thread. There is no `/bench`.

## Drafting

The same steps run locally and on the server (storage specifics in the storage docs):

1. **Expire stale drafts:** release any draft not seen within the expiry (`rostr config expiry`, 7 days by default).
   This is the backstop for sessions that die without the release hook, and it runs lazily here, so there's no
   cleanup job. A dead session holding a name you want can be freed sooner with `rostr bench <name>`.
   Released drafts are kept for 7 days, so a resumed session can ask for its old name back, then deleted.
2. **Same session, same name:** if the session already has an active draft, in any roster, bump its last-seen time
   and return it.
3. **Resume:** if the session had a member before and that member is free, draft them again, from the same roster. A
   resumed session only gets its old name back if it's still free.
4. **Otherwise** draft the free member who was drafted least recently (never drafted first, then roster order), A
   team first, so drafts cycle through the cast instead of repeating a favorite.
5. **Everyone's busy:** take the name of the member who has rested longest (see [Resting](#resting)).
6. **No one's resting:** draft a numbered copy, walking the roster again in the same order: `🧙 Gandalf 2`, then
   `💍 Frodo 2`, and so on (then `3`s). A session always gets a name.

Two sessions starting at once can't get the same member: locally, drafts run in a write transaction; on the server,
unique indexes on active drafts refuse a second draft of the same name, and the losing draft starts over (see
[server-schema.md](server-schema.md#drafting)). The server always decides, so a session never has to be renamed.

### Drafting ahead

In cloud mode a session is drafted when it starts, in the background, rather than on its first prompt, so the first
prompt doesn't wait on the API (see [drafting-latency.md](drafting-latency.md)). `SessionStart` (sources `startup` and
`clear`) starts `rostr hook draft-ahead`, which drafts the session and caches the draft; the agent is told its name on
its first prompt, which reads it from the cache.

- **Idle until the first prompt.** The draft is marked idle: it counts as a turn that ended at the draft, so a session
  opened and never prompted rests after 15 minutes and its name can be taken, and the first prompt records the turn
  starting, like any prompt after a rest. A session idle long enough to lose its name gets a new one on its first
  prompt.
- **Task.** The first prompt sends the start of the prompt as the task in the background.
- **Not there yet.** If the first prompt comes before the draft finishes (a session started with a prompt, say), the
  prompt hook drafts itself, and the server returns the same draft to both.
- **Where it helps.** A terminal session has seconds between starting and its first prompt. A cloud session's prompt
  is already typed when its container starts, so it gets less of a head start. Codex drafts on the first prompt: its
  `SessionStart` fires only when the first prompt is sent, a few milliseconds before `UserPromptSubmit`, in both the
  TUI and `codex exec` (checked with Codex 0.164.0), so drafting at session start would gain nothing there.
- Local mode drafts without a network call, so it drafts on the first prompt.


### Resting

A session rests once it has finished its turn and nobody has sent it a prompt for 15 minutes. It keeps its name and
shows under `Resting:` in `rostr stats`, but when a new session finds no name free, it takes the name of the member who
has rested longest instead of drafting a copy. Fanning out to many sessions reuses the names of ones you've left,
while a session you come back to soon keeps its name. Sessions that never fire `SessionEnd` (a closed desktop session, an
archived cloud session, a terminal left open) rest instead of holding a name until the expiry.

- **Turns.** The `Stop` hook records that the turn ended, and the next prompt's hook, or the next tool call's
  (`PostToolUse`), that the next one started, through `POST /v1/agents/turn` in cloud mode. A turn can start without
  a prompt, when another `Stop` hook continues it or a background task wakes the agent, hence the tool calls. Reports
  run in the background, so they add no wait, and each time only moves forward, so reports that arrive out of order
  can't mark a working session as resting. A session whose `Stop` hook never fires never rests.
- **Coming back.** The `Stop` hook also saves the turn's end and the session's name to
  `~/.cache/rostr/turns/<session-id>`, which stays until a start has been reported, so a failed report is tried again
  on the next prompt or tool call. Tool calls only report in the background, so they never wait on the API. So does a
  prompt within 10 minutes of the turn's end (15, less a margin for a clock behind the server's), since nothing can have
  happened. After that the prompt reports the start and waits for the answer; if the name was taken, the session gets
  its old name back if it's free again, or else a new one, with its task. The title tool calls are left to the title
  hook, which may end the turn at them. The agent is told
  (`rostr renamed this session: you're … now, not …`), and the session is retitled: by the hook where the app shows a
  hook's title, by the agent in Remote Control and cloud sessions.
- A cloud session that resumes after its VM was paused or rebuilt runs `SessionStart` with source `resume`, which
  drafts as usual, asking for its old name back.

### Keep-alive

A draft's last-seen time is bumped by drafting the same session again (resume, compaction, `/draft`), by
`rostr task`, and by the prompt hook, so a session you're using never expires.

- The prompt hook bumps at most once an hour per session, so cloud mode costs at most one API call an hour per
  session rather than one per prompt. It tracks the last bump in `~/.cache/rostr/keepalive/<session-id>` (the file's
  modification time), in both local and cloud mode. Everything else a later prompt needs comes from the session's
  cached draft (`~/.cache/rostr/cloud/sessions/`), so it makes no call.
- If the session's draft has already expired, the bump follows the compaction rule: reclaim the same member if
  they're free, otherwise leave the session unnamed. It never drafts a different member.

## Cloud and keys

- **One key per person, no account.** The rostr key (`rostr_sk_…`) owns all of that person's rosters and project
  links.
- **The CLI generates the key** (256 random bits) when cloud mode is first turned on, and registers by sending only
  the derived auth token. The server never sees the key itself. The key is `rostr_sk_` followed by the 32 bytes as
  base64url without padding.
- **Derived keys.** The CLI derives three 32-byte keys from the rostr key's 32 bytes with HKDF-SHA256 (no salt; info
  labels `auth`, `encrypt`, `fingerprint`):
  - **auth token:** sent base64url-encoded as the bearer token. The server stores only its SHA-256 hash.
  - **encryption key:** encrypts tasks and repo names with AES-256-GCM, a random 96-bit nonce per value. Encrypted
    values are base64: the 12-byte nonce, then the ciphertext with its tag.
  - **fingerprint key:** HMAC-SHA256 over the lowercased `owner/repo`, so the server can match project links without
    repo names. The encrypted copy is what `rostr stats` shows.
- **What's plaintext on the server:** rosters, which members are drafted and when, session IDs.
- **No recovery and no rotation in v1.** A lost key means lost cloud rosters.
- **Trade-offs of encrypting tasks:** API responses contain ciphertext, so web dashboards need the key to show tasks;
  the server still sees task length and timing; and team rosters would need the key shared.
- The API rate-limits key creation by IP.
- **The skill tells the agent never to read `credentials` or `ROSTR_KEY`.** The key isn't very sensitive, so these
  guards prevent accidents rather than stopping a determined agent.

### Public read API

One public endpoint, for web dashboards and integrations in cloud mode. Everything else the CLI calls is internal and
can change.

```
GET https://api.getrostr.sh/v1/stats
Authorization: Bearer <auth token>
```

- **Response:** the same shape as `rostr stats --json`, except each drafted agent's `task` and `repo` are encrypted
  values. Rosters, members, emoji, and who's drafted since when are readable as-is, so a "who's busy" widget needs no
  decryption.
- **Decrypting** takes the recipe in [Derived keys](#cloud-and-keys) and about 15 lines in any language with
  HKDF and AES-GCM (browsers' WebCrypto, Node, Deno, Workers). The site gives it as a prompt to paste to your agent;
  there's no client package in v1.
- **Auth** is the rostr key's derived auth token (`rostr config key --token` prints it). To decrypt tasks and
  repos, the integration also needs your full key. The site says to use it only
  in things you run yourself.
- **CORS** allows any origin, so browser dashboards can call it. Rate-limited per auth token.
- Local mode has no API; `rostr stats --json` covers integrations on your own machine.

## Not in v1

- Windows.
- Team rosters (would need a team key on top of per-person keys).
- Key rotation and recovery.
- `/bench`.
- Moving from cloud back to local (`rostr cloud off`).
- A configurable sign-off format.
- A client package for the API (`@rostr/client`), read-only viewer keys, and write endpoints.
