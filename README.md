# rostr

Give your agents names and remember who's doing what.

rostr drafts a name for every coding agent session you start, from a roster you pick: The Fellowship, Hogwarts,
Smash Bros, or your own list. The name goes at the front of the session title (`🧙 Gandalf · Fix auth redirect loop`),
so when you have five agents running you can tell at a glance who's on what.

It's a small CLI plus a skill that teaches your agent how to use it, and it works with Claude Code and Codex.

> **Status:** early. The CLI below is the planned design. What exists today is the Claude Code session-title hook in
> [`cli/hooks/claude-code/`](cli/hooks/claude-code/).

## How it works

1. **Pick a roster.** `rostr create` sets one up from a preset or your own names.
2. **Every session gets drafted.** At the start of a session, a hook drafts the next available name, tells the agent
   who it is, and puts the name in the session title.
3. **Agents go back to the bench.** The rostr skill has the agent release its name when the work is done, so the
   same names keep coming back.

Every roster has an **A team**, six members by default (change who's on it with `rostr edit`). Drafting always
picks from the A team first and only calls up the **B team** when every starter is busy, so you get to know a small
group instead of twenty names.

You can have **multiple rosters** and give each project its own with `rostr use`, so a name tells you the project as
well as the task. Projects without one use your default roster.

Rosters are either **local** or **cloud**:

- **Local:** state lives on disk. For sessions on your machine.
- **Cloud:** sessions draft from the rostr API at `api.getrostr.sh`. For agents running in the cloud, where there's no
  shared disk.

## Commands

| Command | What it does |
|---|---|
| `rostr create <roster>` | Create a roster from a preset or your own names, with `--local` or `--cloud` |
| `rostr use <roster>` | Use a roster for the current project |
| `rostr draft` | Draft the next available agent, A team first (the hooks run this for you) |
| `rostr list` | See who's on your roster and what each agent is working on |
| `rostr release <name>` | Release an agent back to the roster |
| `rostr edit` | Add, remove, or rename members, pick your A team, or turn emoji on and off |
| `rostr disband` | Delete your roster |

## Install

Point your agent at [getrostr.sh](https://getrostr.sh) and ask it to set up rostr. To do it yourself:

```sh
brew install piazzatron/tap/rostr
rostr create work --preset fellowship
rostr setup   # installs the hooks and skill for Claude Code and Codex
```

Until then, the Claude Code hook can be installed by hand: see [`cli/hooks/claude-code/README.md`](cli/hooks/claude-code/README.md).

## Repo layout

- `cli/` — the `rostr` CLI and agent hooks
- `site/` — getrostr.sh (Astro), auto-deployed to Vercel
- `server/` — api.getrostr.sh, auto-deployed to Railway
- `docs/` — design notes
