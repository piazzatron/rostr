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

Rosters are either **local** or **cloud**:

- **Local:** state lives on disk. For sessions on your machine.
- **Cloud:** sessions draft from the rostr API at `api.getroster.sh`. For agents running in the cloud, where there's no
  shared disk.

## Commands

| Command | What it does |
|---|---|
| `rostr create` | Create a roster from a preset or your own names, with `--local` or `--cloud` |
| `rostr draft` | Draft the next available agent (the hooks run this for you) |
| `rostr list` | See who's on your roster and what each agent is working on |
| `rostr release <name>` | Release an agent back to the roster |
| `rostr edit` | Add, remove, or rename members, or turn emoji on and off |
| `rostr disband` | Delete your roster |

## Install

Point your agent at [getroster.sh](https://getroster.sh) and ask it to set up rostr. To do it yourself:

```sh
brew install piazzatron/tap/rostr
rostr create --preset fellowship
rostr setup   # installs the hooks and skill for Claude Code and Codex
```

Until then, the Claude Code hook can be installed by hand: see [`cli/hooks/claude-code/README.md`](cli/hooks/claude-code/README.md).

## Repo layout

- `cli/` — the `rostr` CLI and agent hooks
- `site/` — getroster.sh (Astro), auto-deployed to Vercel
- `server/` — api.getroster.sh, auto-deployed to Railway
- `docs/` — design notes
