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
- **A turn can't end with no visible text.** Telling the agent to "end your turn without writing anything" made
  Claude Code add "[Your previous response had no visible output. Please continue and produce a user-visible
  response.]", and the agent wrote its answer again (~7s and a duplicate answer). The agent now ends on the new title.
- **Timing.** In a desktop session in Tokyo (cloud mode), the title step took ~14.5s after the answer: the Stop hook's
  API call (~1.9s), two tool calls with a rostr API call each, and the forced continuation. The Stop hook now reads the
  name from its marker file, the task hook runs `async`, and the step is one tool call plus a one-line reply.
- **No hook sees Claude Code's own generated title.** We wanted to prefix the title Claude Code generates (a
  Haiku-written summary of the first prompt) with the name, instead of asking the agent for a summary. The hooks docs
  list no event that fires when a session's title changes, and `session_title` in hook input is only the custom title:
  a generated title "isn't a custom title and doesn't appear in `session_title`". Terminal sessions still write it to
  the transcript as `ai-title` only when no custom title is set, and desktop, Remote Control, and cloud sessions
  generate it server-side, where only Claude can read it (`get_session`). So the agent's own summary stays.
- **Asking after the answer is the wrong shape (v0.1.4).** A desktop session (🎆 Fred, Claude Code 2.1.292) on the
  one-call, async, marker-name version still went badly:
  - The app showed "Stop hook error occurred · ctrl+o to see": a `Stop` hook that exits 2 is reported as an error.
    Claude Code's documented `{"decision": "block", "reason": …}` output (exit 0) might not be; untested.
  - The session's last message, and its `result`, became the title line ("🎆 Fred · Is kiwi a fruit") instead of the
    answer. Anything the agent writes after its answer replaces the answer as the last thing shown, and writing
    nothing forces a continuation, so this comes with asking after the answer.
  - It took ~11s after the answer: ~1.5s of Claude Code's own work before the `Stop` hook ran (the hook took ~30ms),
    ~7.2s for the model to make the `set_session_title` call (time to first token on the continuation), and ~2s for
    the title line.
  - The alternative: ask for the title in the first prompt's `additionalContext` ("as part of your first reply, call
    `set_session_title`…; if you call other tools first, include it in that batch"). The answer stays last, there's no
    continuation and no hook error, and a first tool call costs nothing extra; an agent that would answer straight
    away pays one quick tool call first. The `Stop` hook would stay only as a backup, with the `PostToolUse` hook
    clearing the pending marker when the agent's call succeeds.
- **What #103336 would fix.** Once a hook's `sessionTitle` reaches the app, the prompt hook's `<name> · <start of the
  prompt>` shows in the app before Claude answers, with no agent involvement. The whole title step (the `Stop` hook,
  the follow-up turn, the hook error, the title as last message) only exists to get the name into the app, so it can
  go. A summary instead of the start of the prompt would still need the agent: it sets its task with
  `rostr agent task`, and the next prompt's hook retitles the session, as terminal sessions do today. Until then the
  title shows the first ~50 characters of the prompt, which reads badly when a prompt starts with pasted text. The
  fix isn't in 2.1.292 (desktop) or 2.1.306 (cloud) as of October 21.
- **The app's titles are readable locally, on the desktop app (correction).** "Only Claude can read the server title"
  was wrong for the desktop app. Its web view caches the claude.ai session list in IndexedDB
  (`~/Library/Application Support/Claude/IndexedDB/https_claude.ai_0.indexeddb.blob/…` on macOS), as Chromium's
  snappy-compressed V8 serialization (header `ff 11 02`). Decompressed, it holds each session's server ID and current
  title, rostr's ("🎆 Fred · Is kiwi a fruit", "🌙 Luna · Find latest modified folder") and the app's own generated
  ones alike. Older desktop session files (`claude-code-sessions/<account>/<org>/local_*.json`, last written
  October 19) also carried `title`, `titleSource: "auto"`, and `cliSessionId`, the session ID hooks get.
  Caveats: it's undocumented internal storage that any app update can change; the blob file is rewritten (and renamed)
  as the app refreshes, so how soon a new title lands there is untested; it only exists where the desktop app runs (not
  in cloud containers or terminal-only setups); and it's the user's whole session list. It also only helps with
  reading: a hook still can't write the app's title until #103336 ships, so today the agent's `set_session_title`
  call stays either way. Once #103336 ships, a hook could prefix the app's generated title with the name on a later
  prompt, if the app still generates one for a session a hook has already titled (untested).

## Final design (v0.1.5)

The agent titles the session during its reply, and a `PostToolUse` hook ends the turn when that reply was a plain
answer. Findings that led there, all on October 21 (desktop app with Claude Code 2.1.292 for Remote Control sessions
and 2.1.304 for Local ones):

- **Local and Remote Control sessions differ.** Their `~/.claude/sessions/<pid>.json` says `entrypoint:
  claude-desktop` (with a `hostSessionId`) for a session the app runs as Local, and `sdk-cli` (run in tmux by the
  Remote Control host) otherwise. Local sessions also get a server ID when Remote Control is on. The app adopts a
  hook's `sessionTitle` for Local sessions (`takeEngineReportedTitle`, ~3s after the prompt) and refuses it for
  Remote Control ones (`remoteControlSpawn`). Local sessions have the app's own title tool,
  `mcp__ccd_session_mgmt__set_session_title` (deferred, so loaded with ToolSearch; `session_id: "self"`; it replaces
  titles the app generated, or a hook's, without asking, and asks only when the user named the session; it sets
  `titleSource: "tool"`, after which the app ignores hook titles). Remote Control and cloud sessions have
  `mcp__claude-code-remote__set_session_title` instead.
- **The app's own summary is ready ~1s after the first message but readable only after ~4-5s.** The app's web view
  generates it, independently of hooks, before Claude Code has even started (its startup took 2-6s in these tests).
  The main process keeps it in memory and saves `claude-code-sessions/<account>/<org>/<host session>.json` on a
  debounce (`SAVE_SESSION_ACTIVE_DEBOUNCE_MS` = 3s while running), where it stays in `previousTitles` after a later
  rename. Recording every file the app wrote after a message found no earlier copy. It reaches Claude Code ~70ms
  after the first-prompt hook finishes, so a hook waiting for it only delays it.
- **No hook can set a title after the first prompt without a prompt.** A background (`asyncRewake`) wake-up is held
  until a text-only turn ends. On the wake-up prompt, `decision: block` discards `sessionTitle` (documented) and shows
  the user "A hook blocked your prompt", and `continue: false` discards it too ("Operation stopped by hook"). Letting
  it through applies the title, but Claude must then write something: a reply with only thinking gets "[Your previous
  response had no visible output. Please continue and produce a user-visible response.]", and Claude repeats its
  answer. `mcp_tool` hooks can only pass values from their own input, so they can't compose a per-session title.
  Appending a `custom-title` line to the transcript doesn't work: Claude Code writes its own title back after it.
- **A `PostToolUse` hook can end the turn.** Returning `continue: false` after a tool call ends the turn without
  Claude replying to the result and without anything visible. rostr does that only when every tool call in the turn
  was titling and Claude has written text. Claude Code writes an assistant message to the transcript once the message
  is complete, which can be after its first tool call ran, so the hook waits up to 0.5s for the call to appear.
- **The name can't wait for the agent.** On a real first task, the title call comes after the agent's thinking, the
  ToolSearch round trip, and its first tool calls, or after a whole plain answer: up to a minute or more. So Local
  sessions get the first-prompt hook's `<name> · <start of the prompt>` title too (the app adopts it ~3s after the
  prompt), at the cost of a third title: the app's summary, then that, then the agent's.
- **Terminal sessions have no title tool,** and Claude Code skips its own summary once a hook has titled the
  session (the generator runs only when there's no title yet), so asking the agent to run `rostr task` in its first
  reply was the only summary route. In an interactive terminal that call races the transcript write: the message
  holding the call is written only after the `PostToolUse` hook exits, so the end-turn check can't see the answer and
  the agent answers twice (`claude -p` and the app write it before the hook). So the terminal summary now comes from
  rostr: the first-prompt hook starts a background `claude -p --model haiku` run on the user's login (~5–8s, hooks
  and settings off), saves the result as the task, and the next prompt's hook titles the session with it.
- **The app's own summary can't be borrowed for the first-prompt title.** Measured on Oct 23 by a background probe
  watching Claude Code's session registry (`~/.claude/sessions/<pid>.json`) and the app's session file every 100ms:
  the hook's title was in the registry at +6ms (`nameSource: hook`); the app's summary never entered the registry
  at all, and first became readable at +4.7s, when the app's session file was first written, with the hook's title
  already adopted and the summary in `previousTitles`. Nothing a hook could wait for short of ~5s.
- **The title call goes after the answer, always.** An earlier rule had tool-using replies title in their first
  batch of calls, so the summary wouldn't wait on a long task; in practice the title changed before anything was
  answered (Oct 23), which read wrong, and the name is instant from the hook anyway. The end-turn check accepts work
  before the answer: it ends the turn when every tool call since the answer's text was titling.
- **Except in Remote Control and cloud sessions, which title first.** Opus 5.5 (and Fable, Sonnet 5.5) can return
  text written just before a tool call as a "progress update": a `thinking` block holding a server-written summary,
  not a `text` block ([thinking docs](https://platform.claude.com/docs/en/build-with-claude/thinking#progress-updates)).
  The app and claude.ai show these for Remote Control and cloud sessions. In a cloud session on Oct 23 (💍 Frodo,
  "who's currently drafted?"), the answer before the title call came back as a two-sentence summary; the end-turn
  check saw no answer text, the hook told Claude to carry on, and Claude wrote the answer again as a table. Without
  the title call that turn had no progress update at all. The first thing in a turn is never turned into one, and
  text after the last tool call almost never is (an analysis on anthropics/claude-code#94336 counted 4,204 kept as
  text and 3 converted, against 23 and 19 just before the Desktop's end-of-turn tool), so these sessions make the
  title call before writing anything, and the answer ends the turn as usual. The cost is the title changing before
  the answer appears. Local sessions are exposed the same way but keep titling after the answer for now.
- **Measured:** a Local plain answer ("deepest lake") answered at +5.3s, the same as without titling, with the title
  at +11s and no second reply. With a tool-using first turn the title call went alongside `ls` and the turn carried
  on. A Remote Control session worked the same way.
