# Claude Code session titles: how the hook ended up this way

The goal: when a Claude Code session starts, draft an agent name for it (say "Otter"), tell Claude its name,
and show that name in the session title, e.g. `Otter · Vite Tailwind build error`.

Requirements, in the order they came up:

1. Works in both plain terminal sessions and desktop app / Remote Control sessions.
2. The title updates after the **first** message, not the second.
3. No added latency before Claude starts answering.
4. Deterministic where possible, and safe to publish: no reading users' credentials, no assuming tmux.

The final design is in [`cli/hooks/claude-code/`](../cli/hooks/claude-code/). This doc covers how we got there.

## Key findings

**Claude Code keeps two separate titles.**
- The *local* title lives in the session transcript (`~/.claude/projects/<dir>/<session>.jsonl`) as
  `{"type":"custom-title","customTitle":...}` lines. Claude Code's own auto-generated title is an
  `{"type":"ai-title","aiTitle":...}` line. This is what terminal sessions show.
- Desktop / Remote Control sessions also have a *server-side* title, which is what the app's sidebar shows.
  The app generates its own (e.g. "Biggest dinosaur") a few seconds after the first message.

**Hooks can only set the local title.** `SessionStart` and `UserPromptSubmit` hooks can return
`hookSpecificOutput.sessionTitle`. The docs say it has "the same effect as `/rename`", but it doesn't:
`/rename` updates the app's title, and `sessionTitle` only writes the local one. This looks like a bug.

**Only Claude Code holds the authenticated connection that sets the app's title.** In Remote Control
sessions Claude has a `mcp__claude-code-remote__set_session_title` tool, and calling it renames the session in
the app straight away. But:
- It only accepts the server session ID (`session_…` or `cse_…`). Hook input only carries the local session
  UUID, and the tool rejects that, as well as `@self`.
- The server ID can be found locally: Claude Code writes `~/.claude/sessions/<pid>.json` for each running
  session, with both `sessionId` (local) and `bridgeSessionId` (server). That format is undocumented.

## What we tried

| Attempt | Result |
|---|---|
| `SessionStart` / `UserPromptSubmit` hook returning `sessionTitle` | Sets the local title. Never reaches the desktop app. |
| Read Claude's auto title from the transcript (`ai-title`) and prefix it on a later prompt | Works locally, but only from the second prompt on, and still doesn't reach the app. |
| A background script that sleeps, waits for the title, then edits it | No way to deliver the result: a hook's output only counts while Claude Code waits on it, and there's no CLI command or public API for renaming a running session from outside. |
| Claude calls `set_session_title` itself (via `additionalContext`) | Renames the app's title on the first message. |
| Summarize the prompt with `claude -p --model haiku` inside the hook | Good titles on the first message, but adds ~5s before Claude starts. The child `claude -p` also fires the same hook, so it needs a recursion guard. |
| Same hook with `"async": true` | Async hooks' `sessionTitle` is ignored, and their output only reaches Claude with the *next* prompt. The hook ran twice and renamed the session twice. |
| Claude calls `get_session` to read the app's title, then `set_session_title` with the name prefixed | Works, but each tool call costs a model round trip: ~20s before the answer started. |
| `mcp_tool` hook calling `set_session_title` directly | Input can only be interpolated from the hook's own JSON input, which has no server session ID. Hooks on the same event run in parallel, so another hook can't look the ID up first. |
| Our own MCP server that looks up the ID and calls the rename tool | Lookup works, but MCP servers can't call another server's tools, so ours would have to call Anthropic's API with the user's credentials. |
| Call the server API directly from a background script | Would need the user's Claude sign-in token and an undocumented endpoint. Not something to ship in a public tool. |
| Type `/rename` into the session's tmux pane | Can't assume users run Claude Code in tmux. |
| Agent SDK `renameSession()` | Documented as appending a `custom-title` entry, the same local-only mechanism as `sessionTitle`. |
| `asyncRewake` hook | **Works.** See below. |

## Final design

Two `UserPromptSubmit` entries run the same script in different modes, both only on a session's first prompt:

1. **`name`** (normal hook, finishes instantly): tells Claude "For this session you've been drafted as Otter"
   and sets the local title to `Otter · <start of the prompt>`. This covers terminal sessions.
2. **`rename`** (`asyncRewake`): in desktop / Remote Control sessions, looks up the server session ID, waits 5
   seconds in the background, then exits with code 2. That wakes Claude with an instruction to call
   `set_session_title` with `Otter · <its own 3-5 word summary>`.

In testing, Claude's answer started at the usual speed (~6s) and the app's title changed ~3s later, with no
second message and no visible extra reply.

Two details matter:

- **Why two entries:** a hook is one process that exits once. Telling Claude its name and setting the
  terminal title need a hook that finishes *before* Claude starts. Waking Claude later needs one that
  finishes *after*. They write different titles (local vs. app), so nothing is renamed twice.
- **First-prompt detection** reads the transcript for an earlier prompt typed by the user
  (`origin.kind == "human"`), instead of keeping marker files. Marker files would treat every session that
  existed before the hook was installed as new and rename it.

The name is derived from a hash of the session ID, so both modes pick the same one without sharing state.

## Open issues

- The desktop rename depends on Claude following an instruction.
- It relies on the undocumented `~/.claude/sessions/*.json` format.
- Either of these Claude Code changes would let the `rename` mode go away:
  1. A hook's `sessionTitle` reaches the app, as the docs already say it does.
  2. Hook input includes the server session ID in Remote Control sessions, so an `mcp_tool` hook can call
     `set_session_title` directly.
