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

## Update: October 20 (Claude Code 2.1.292)

- **The bug is fixed upstream but not released.** We filed it as
  [anthropics/claude-code#103336](https://github.com/anthropics/claude-code/issues/103336); it was closed as completed
  on October 19. On 2.1.292 a `UserPromptSubmit` hook that always returns `sessionTitle` still only sets the local
  title: the session's `~/.claude/sessions/<pid>.json` showed `"name": "🧪 Hook title · 21:57:35"` while `get_session`
  on its bridge session still returned the app's old title.
- **Which hooks can set a title.** Per the current hooks docs, only `SessionStart` (sources `startup`, `resume`,
  `fork`) and `UserPromptSubmit`. `Stop`, `PostToolUse`, and async or `asyncRewake` hooks can't. So nothing can set
  the title after Claude's first reply except the next prompt's hook, or Claude itself (`set_session_title`).
- **Claude's own summary title lives in different places.**
  - Terminal sessions write it locally as an `ai-title` entry in the transcript, but **only if no custom title is
    set**: every session whose title a hook set at the first prompt has no `ai-title` at all.
  - Desktop and Remote Control sessions never write it locally. The app generates it server-side; the transcript has
    no `ai-title`. Only Claude can read it, with `get_session` (one model turn each, which is why the earlier
    `get_session` + `set_session_title` approach took ~20s).
- **The research session** behind this doc is `552f74fc-070d-5d06-aa53-7ff8a8a2687b` (October 19), in
  `~/development`.
- **Testing without the app.** A session created with the `create_session` tool runs on this machine through the
  Remote Control host and shows in the app, and `get_session` reads its server title. In two such sessions on 2.1.292,
  the hook's `sessionTitle` set only the local title, and a session created without a title still had none ~60s after
  its reply: the app's own auto title (e.g. "Biggest dinosaur") comes from the desktop app when a person types the first
  message there, so this harness can't reproduce the race with it. The server also keeps a per-turn
  `post_turn_summary` (e.g. "Mount Fuji is Japan's tallest at 3,776m"), readable only through `get_session`.

## Update: October 21 (Claude Code 2.1.306)

- **Cloud sessions behave like desktop ones.** A cloud session's hook `sessionTitle` only sets the local title too:
  a session created without a title still had no server title after its reply. Cloud sessions have no
  `~/.claude/sessions/*.json` bridge entry, but the container sets `CLAUDE_CODE_REMOTE_SESSION_ID=cse_<id>`, and
  `set_session_title` accepts the same ID as `session_<id>`.
- **The background wake-up could land before Claude answered.** Claude Code delivers an `asyncRewake` hook's message
  at the next point it can. When the first turn starts with tool calls, that's right after the first one, so "end
  your turn without writing anything" arrived before any answer (labelled "Stop hook blocking error from command
  UserPromptSubmit"). The title step moved to a `Stop` hook, which only runs once the turn has finished. `Stop` hooks
  can't return `sessionTitle`, but the agent sets the title itself anyway.
- **One tool call instead of two.** Asking the agent to call `set_session_title` and then run `rostr agent task` took
  two model round trips. A `PostToolUse` hook (matcher `mcp__claude-code-remote__set_session_title`) gets the title the
  agent passed in `tool_input`, so it records the summary as the task itself. `PostToolUse` only runs when the tool
  call succeeds, so the task matches a title the app actually has.
- **No hook sees Claude Code's own generated title.** We wanted to prefix the title Claude Code generates (a
  Haiku-written summary of the first prompt) with the name, instead of asking the agent for a summary. The hooks docs
  list no event that fires when a session's title changes, and `session_title` in hook input is only the custom title:
  a generated title "isn't a custom title and doesn't appear in `session_title`". Terminal sessions still write it to
  the transcript as `ai-title` only when no custom title is set, and desktop, Remote Control, and cloud sessions
  generate it server-side, where only Claude can read it (`get_session`). So the agent's own summary stays.
