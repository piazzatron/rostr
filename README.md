# rostr

Give your agents names and remember who's doing what.

rostr drafts a name for every coding agent session you start, from a roster you pick: The Fellowship, Hogwarts,
Smash Bros, or your own list. The name goes at the front of the session title (`🧙 Gandalf · Fix auth redirect loop`),
so when you have five agents running you can tell at a glance who's on what.

It's a small CLI plus a skill that teaches your agent how to use it, and it works with Claude Code and Codex.

> **Status:** early. The CLI below is the planned design ([spec](docs/cli-spec.md)). What exists today is the
> Claude Code session-title hook in [`cli/hooks/claude-code/`](cli/hooks/claude-code/).

## How it works

1. **Pick a roster.** `rostr assemble` sets one up from a preset or your own names.
2. **Every session gets drafted.** At the start of a session, a hook drafts the next available name, tells the agent
   who it is, and puts the name in the session title.
3. **Agents go back to the bench.** When a session ends, a hook releases its name, so the same names keep coming
   back.

Every roster has an **A team**, six members by default (change who's on it with `rostr edit`). Drafting always
picks from the A team first and only calls up the **B team** when every starter is busy, so you get to know a small
group instead of twenty names.

You can have **multiple rosters** and give each project its own with `rostr link`, so a name tells you the project as
well as the task. A project is identified by its git `origin` remote; projects without a roster, and folders outside
git, use your default roster.

Your whole setup lives in one place, **local** or **cloud**:

- **Local** (the default): everything lives on disk. No account, no network.
- **Cloud**: `rostr config cloud on` moves everything to the rostr API at `api.getrostr.sh`. From then on, every session, on
  your machine or in a cloud environment like Claude Code on the web or Codex cloud, drafts from there. Having a rostr
  key is what puts a machine in cloud mode.

## Commands

Full docs at [getrostr.sh/cli](https://getrostr.sh/cli).

**Roster commands** (you run these)

| Command | What it does |
|---|---|
| `rostr assemble` | Interactive setup: pick a roster, emoji, local or cloud, and auto-draft. The only one you need |
| `rostr link <roster>` | Link a roster to the current project (`rostr unlink` to undo) |
| `rostr list` | See who's working on what across your rosters, and who's idle |
| `rostr edit [<roster>]` | Change a roster's members, emoji, or A team |
| `rostr presets` | See all the pre-built rosters |
| `rostr disband <roster>` | Delete a roster |

**Agent commands** (your hooks, the skill, and the `/draft` slash command run these)

| Command | What it does |
|---|---|
| `rostr agent draft` | Draft the next free agent for a session, A team first |
| `rostr agent release` | Send an agent back to the bench |
| `rostr agent task` | Set or change what an agent is working on |

**Settings**

| Command | What it does |
|---|---|
| `rostr config` | See all your settings |
| `rostr config default <roster>` | Choose the roster projects use when they don't have a link |
| `rostr config emoji on\|off\|only` | Show agents with their emoji, without it, or as just the emoji |
| `rostr config auto-draft on\|off` | Draft every new session automatically, or by hand with `/draft` |
| `rostr config cloud on` | Move your whole setup to the cloud |
| `rostr config key` | Print your rostr key |

## Install

Point your agent at [getrostr.sh](https://getrostr.sh) and ask it to set up rostr. To do it yourself:

```sh
brew install piazzatron/tap/rostr
rostr assemble   # interactive: pick a roster, emoji, local or cloud, and auto-draft
```

Prefer to draft by hand? Turn off auto-draft (`rostr config auto-draft off`) and use `/draft` in Claude Code.

Until then, the Claude Code hook can be installed by hand: see [`cli/hooks/claude-code/README.md`](cli/hooks/claude-code/README.md).

## Repo layout

- `cli/` — the `rostr` CLI and agent hooks
- `site/` — getrostr.sh (Astro), auto-deployed to Vercel
- `server/` — api.getrostr.sh, auto-deployed to Railway
- `docs/` — design docs; [`docs/cli-spec.md`](docs/cli-spec.md) is the source of truth for the CLI
