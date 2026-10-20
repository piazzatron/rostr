# Integrations research: Codex, herdr, tmux

How rostr could name sessions in Codex, herdr, and tmux, measured against the Claude Code design in
[cli-spec.md](cli-spec.md#hooks-skill-and-slash-command) and [claude-code-session-titles.md](claude-code-session-titles.md).

Researched 2026-10-20 against Codex CLI `0.162.0-alpha.17.2` (installed locally inside the ChatGPT desktop app),
the Codex docs at learn.chatgpt.com (the old developers.openai.com/codex URLs redirect there), herdr `0.9.5`
(released 2026-10-14), tmux `3.8`, and Claude Code `2.1.304`. Facts are linked. Anything marked
**(inferred)** comes from reading the docs or source and hasn't been tested.

## Summary

| | Naming / label | Session title | Hooks | Context injection | Session ID |
|---|---|---|---|---|---|
| **Claude Code** (for reference) | n/a | `sessionTitle` (local only), `set_session_title` tool (app) | Full set, including `asyncRewake` | `additionalContext`, `CLAUDE_ENV_FILE` | `session_id`, stable across resume |
| **Codex** | n/a | No hook output for titles. `/rename` in the TUI, and app-server `thread/name/set` from outside | `SessionStart`, `UserPromptSubmit`, `Stop`, `SessionEnd`, compaction events, and more. `async: true`, but no rewake | `additionalContext` (or plain stdout) on `SessionStart` and `UserPromptSubmit`. **Hooks can't set env vars**, but Codex already exports `CODEX_THREAD_ID` to agent commands | `session_id` = thread ID, stable across resume; a fork gets a new one |
| **herdr** | `herdr pane report-metadata --display-agent "🧙 Gandalf"` replaces "claude"/"codex" in the sidebar. `herdr agent rename` takes slugs only | `report-metadata --title`. OSC titles are kept but not shown by default | Agents report to herdr through its CLI or socket; herdr also has plugins with event hooks | n/a | n/a (it stores each agent's session ID for resume) |
| **tmux** | `rename-window`, `select-pane -T`, or a pane user option (`@rostr`) | The pane title (`#T`) comes from the agent's OSC 0/2 writes and shows in the default `status-right` | n/a | n/a | n/a |

**Biggest catch:** since 0.157, Codex runs hooks inside a shared app-server daemon, so a hook can't tell which
terminal pane its session is in ([openai/codex#48500](https://github.com/openai/codex/issues/48500), still open).
In Codex, anything rostr does from a hook against tmux or herdr lands on the wrong pane. The terminal title still
works, because the Codex TUI writes it from the right pane itself.

## Codex

### Hooks: confirmed, close to Claude Code's

Source: [Hooks docs](https://learn.chatgpt.com/docs/hooks).

- **Events:** `SessionStart` (matcher on `source`: `startup|resume|clear|compact|fork`), `UserPromptSubmit`,
  `Stop`, `SessionEnd`, `PreCompact`/`PostCompact`, `Pre/PostToolUse`, `PermissionRequest`, `SubagentStart/Stop`,
  `Interrupt`.
- **Config:** `~/.codex/hooks.json` or `[hooks]` in `~/.codex/config.toml`, plus `<repo>/.codex/hooks.json` or
  `<repo>/.codex/config.toml` (project hooks load only in trusted projects). Plugins can bundle hooks too. All
  layers merge. Hooks are on by default (`[features] hooks`; the old name `codex_hooks` is deprecated).
- **Trust review:** Codex skips any non-managed hook until the user approves it in `/hooks`. Approval is tied to a
  hash of the hook definition, so a changed hook needs approving again. rostr's one-line `rostr hook codex …`
  commands keep the same hash across rostr upgrades, so users approve once. The installer must tell users to
  open `/hooks`, or nothing runs.
- **Input:** every hook gets `session_id`, `transcript_path`, `cwd`, `hook_event_name`, and `model`; most also get
  `permission_mode`. `UserPromptSubmit` adds `prompt` and `turn_id`, and `SessionStart` adds `source`.
  `session_id` is a `ThreadId` ([`hooks/src/types.rs`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/types.rs)).
- **Context:** for `SessionStart` and `UserPromptSubmit`, plain stdout or
  `hookSpecificOutput.additionalContext` becomes developer context (capped at about 2,500 tokens by default). After
  compaction, the matching `SessionStart` hook (`compact`) runs before the next model request, even when
  compaction happens mid-turn.
- **Env vars:** hooks can't set env vars for the agent. But Codex injects **`CODEX_THREAD_ID`** into every
  command the agent runs
  ([`core/src/exec_env.rs`](https://github.com/openai/codex/blob/main/codex-rs/core/src/exec_env.rs)). herdr's
  Codex hook treats it as equal to the hook's `session_id`
  ([script](https://github.com/herdrdev/herdr/blob/master/src/integration/assets/codex/herdr-agent-state.sh)). So
  rostr can fall back to `CODEX_THREAD_ID` when `ROSTR_SESSION` is unset, and Codex needs no env-file step.
- **Async:** `async: true` runs a hook in the background. Its `additionalContext` reaches the next model request
  in the current turn, or the next user turn if the turn has ended. **It never starts a turn**, so there's no
  `asyncRewake` equivalent. That doesn't matter much: on Codex, rostr can set the title itself (see Titles), so
  it doesn't need to wake the model.
- **`SessionEnd`:** fires on normal exit, on archiving or deleting an open thread, or after 30 minutes idle with
  no client attached. It always runs synchronously, with a 1-second timeout by default and 3 at most. `reason` is
  always `other`. A release that waits on the network needs `timeout: 3` and a shorter internal timeout, or it
  should hand off to a detached process.
- **Shared daemon (0.157+):** interactive TUIs connect to a per-`CODEX_HOME` app-server daemon by default
  ([changelog 0.157.0](https://learn.chatgpt.com/docs/changelog), "automatic background-server startup"). Hooks
  and tool calls run in the daemon with **the environment of whichever terminal started the daemon**, so
  `TMUX_PANE`, `HERDR_PANE_ID`, and the like are stale or wrong
  ([#48500](https://github.com/openai/codex/issues/48500); proposed fix
  [#44902](https://github.com/openai/codex/issues/44902), open). The workaround is `codex --no-daemon` or
  `[features] daemon_auto_start = false`, which breaks remote control. `session_id` and `CODEX_THREAD_ID` are
  still correct.
- **`notify`:** the legacy `notify = [...]` command gets a JSON payload after each turn. Hooks cover everything
  it does, so rostr doesn't need it.

### Session ID and resume

- The thread ID is a UUID (v7). `codex resume <id|name>` and `codex resume --last` keep the ID, and the hook gets
  `source: resume`. A fork gets a new ID with `source: fork`. That matches rostr's resume and compaction rules
  as they are.
- **(inferred)** `/clear` and `/new` start a new thread (`source: clear`). Check whether `SessionEnd` fires for
  the old thread; if not, the 7-day expiry is the backstop.

### Titles

- Threads have a user-facing name. The TUI's `/rename` sets it
  ([slash commands](https://learn.chatgpt.com/docs/developer-commands)), and so does the app-server's
  `thread/name/set {threadId, name}`, which emits `thread/name/updated`
  ([app-server docs](https://learn.chatgpt.com/docs/app-server),
  [`ThreadSetNameParams`](https://github.com/openai/codex/blob/main/codex-rs/app-server-protocol/schema/typescript/v2/ThreadSetNameParams.ts)).
  Names land in `~/.codex/session_index.jsonl` (`thread_name`), and **(inferred)** Codex fills them in
  automatically (local entries read like generated titles).
- **Hooks have no title output** (no `sessionTitle`), and the model has no rename tool.
- **What would work:** rostr calls `thread/name/set` itself, from the hook (which runs outside the agent's
  sandbox), over the daemon's control socket (`codex app-server proxy` pipes stdio to it). **(inferred)** One call
  renames the thread everywhere the daemon serves it: the TUI, `codex agents`, and the ChatGPT desktop app and IDE
  extension, if they share the daemon. Without the daemon (`--no-daemon`) there's no socket to reach, and the
  title can't be set from outside.
- **Terminal title:** the TUI writes OSC titles from `tui.terminal_title`. The default is
  `["spinner", "project"]`, and the item list includes `thread-name` ("omitted when unnamed")
  ([config reference](https://learn.chatgpt.com/docs/config-file/config-reference),
  [`title_setup.rs`](https://github.com/openai/codex/blob/main/codex-rs/tui/src/bottom_pane/title_setup.rs)).
  With `["spinner", "thread-name", "project"]`, the rostr name reaches tmux and herdr from the right pane, even
  under the daemon. Under GNU Screen, Codex converts titles to ASCII, which drops emoji.

### Skills and slash commands

- **Custom prompts (`~/.codex/prompts`) are deprecated and removed in current builds**
  ([custom prompts](https://learn.chatgpt.com/docs/custom-prompts)). The local install has no `prompts` dir.
  There's no user-defined `/draft`.
- **Skills:** a folder with a `SKILL.md` (`name`, `description`) in `$HOME/.agents/skills`, in
  `.agents/skills` anywhere from the cwd up to the repo root, or in `/etc/codex/skills`
  ([build skills](https://learn.chatgpt.com/docs/build-skills)). The user invokes one with `$name` or the model
  picks it implicitly. `~/.codex/skills` still has entries on this machine, but the docs list `~/.agents/skills`.
- So `/draft` becomes a skill, `$rostr-draft`, whose body runs `rostr hook codex draft`. **(inferred)** A Codex
  plugin could bundle the hooks and both skills into a single install.

### Cloud

- Codex Cloud environments are set up conversationally, with an install script, a "start skill", environment
  variables, network secrets, and a domain allowlist
  ([cloud environments](https://learn.chatgpt.com/docs/environments/cloud-environments)).
- `ROSTR_KEY` must be a plain **environment variable**, not a network secret. A network secret only gives the
  program a placeholder that the proxy swaps in for HTTPS headers, and rostr derives its auth token and
  encryption key from the raw key locally.
- `api.getrostr.sh` has to be on the allowlist, and the install script has to install rostr.
- **Unknown:** whether cloud tasks run repo `.codex/hooks.json` hooks. The docs say cloud orchestration for
  Work Cloud ignores command hooks, and they don't cover Codex Cloud tasks.

### Recommended minimal Codex integration

1. **Hooks** in `~/.codex/hooks.json`, each one `command -v rostr >/dev/null && rostr hook codex <event> || true`:
   - `SessionStart` with `resume|compact` re-tells the agent its identity through `additionalContext`.
   - `UserPromptSubmit` (synchronous) runs the draft step on the first prompt: draft, return the identity text,
     and kick off the rename below in the background. On later prompts it does the hourly keep-alive.
   - `SessionEnd` (`timeout: 3`) releases the name.
2. **Session:** default `--session` to `ROSTR_SESSION`, then `CODEX_THREAD_ID`. There's no env file.
3. **Title:** rostr calls `thread/name/set` with `🧙 Gandalf · <task>` through the daemon socket. The task is the
   prompt's start, or Codex's own thread name if one exists by the first `Stop`. Skip this if no daemon is
   running.
4. **Terminal title:** the installer suggests `tui.terminal_title = ["spinner", "thread-name", "project"]`, so
   tmux and herdr see the name.
5. **Skill** in `~/.agents/skills/rostr/`, plus a `rostr-draft` skill in place of `/draft`.
6. **Install output** tells the user to approve the hooks in `/hooks`.

## herdr

[herdr](https://github.com/herdrdev/herdr) ([herdr.dev](https://herdr.dev)) is a terminal workspace manager for
coding agents, written in Rust (Apache-2.0, about 45k stars, v0.9.5). It's a tmux-like server with workspaces,
tabs, and panes. It detects agents in panes (Claude Code, Codex, and 20 or so more), shows each one's
`idle/working/blocked/done` state in a sidebar, rolls state up to tabs and workspaces, sends notifications, and
restores agent sessions after a restart. It's installed locally at `/opt/homebrew/bin/herdr`.

### How it shows agents

- Every pane process gets `HERDR_ENV=1`, `HERDR_PANE_ID` (like `w1:p1`), `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID`,
  `HERDR_SOCKET_PATH`, and `HERDR_BIN_PATH` ([add herdr support](https://herdr.dev/docs/add-herdr-support/)).
- The default sidebar rows are `[["state_icon","machine","workspace","tab"],["agent"]]`, where `agent` is the
  agent label ("claude", "codex"). The latest OSC 0/2 title is kept as `terminal_title` and
  `terminal_title_stripped`, but **neither is shown unless the user adds it to `[ui.sidebar.agents] rows`**
  ([agents](https://herdr.dev/docs/agents/), `herdr --default-config`).
- **So rostr names don't show up on their own.** Claude Code's title is its own label, and Codex's default title
  doesn't include the thread name.

### Ways to label a pane

- **`herdr pane report-metadata`** is display-only, so it never takes over state, waits, or resume from herdr's
  own integration ([integrations: custom status labels](https://herdr.dev/docs/integrations/#custom-status-labels),
  [socket API](https://herdr.dev/docs/socket-api/)):
  ```
  herdr pane report-metadata "$HERDR_PANE_ID" --source rostr --agent claude \
    --display-agent "🧙 Gandalf" --title "Fix auth redirect loop" --token rostr="🧙 Gandalf"
  ```
  - `--display-agent` replaces the visible agent name. `--title` overrides the pane title. `--token` sets a
    `$rostr` value users can put in custom rows.
  - Values are trimmed, stripped of control characters, and capped at 80 characters. `--agent` makes the
    presentation fields apply only while that agent owns the pane.
  - Without `--ttl-ms`, the metadata stays until it's replaced, cleared (`--clear-display-agent`,
    `--clear-title`), or the pane closes. It isn't restored after a herdr server restart, so the `resume` hook
    has to report it again.
  - `--source` must not start with `herdr:`.
- **`herdr agent rename <pane> <name>`** names must match `[a-z][a-z0-9_-]{0,31}` and be unique among live
  agents, so "gandalf" or "gandalf-2" works but emoji don't. The name also becomes a CLI target
  (`herdr agent prompt gandalf …`), and herdr clears it when the agent exits. It's a nice extra, but
  `--display-agent` is the right display hook.
- **Plugins** (TOML manifest, actions, event hooks, panes) and the socket API exist
  ([plugins](https://herdr.dev/docs/plugins/)). rostr doesn't need either, because the CLI covers this.

### Gotchas

- **Codex:** hooks run in Codex's shared daemon, so `HERDR_PANE_ID` is the first pane's, or missing. herdr's own
  Codex integration is broken by this ([herdr#4649](https://github.com/herdrdev/herdr/issues/4649), open;
  workaround `codex --no-daemon`). For Codex in herdr, either skip `report-metadata` or call it only when the
  env is trustworthy, and rely on the `thread-name` terminal title plus a `terminal_title_stripped` sidebar row.
- **Claude Code:** hooks run as children of the `claude` process, so `HERDR_PANE_ID` is correct
  **(inferred** from herdr's own Claude integration working; not tested with Claude background sessions).
- herdr can run inside tmux. **(inferred)** Agents in herdr panes don't see the outer `TMUX`: herdr's docs
  suggest gating tmux auto-attach on `HERDR_ENV`, which implies `TMUX` is unset in its panes. Either way, when
  `HERDR_ENV=1`, rostr should label through herdr and leave tmux alone.

### Recommended minimal herdr integration

There's no plugin. Inside the existing `rostr hook claude-code …` handlers:

- **Draft, resume, or compact:** when `HERDR_ENV=1` and `HERDR_PANE_ID` is set, run
  `"$HERDR_BIN_PATH" pane report-metadata "$HERDR_PANE_ID" --source rostr --agent claude --display-agent "<display>"`
  in the background with a short timeout, ignoring failures. Optionally add `--title "<display> · <task>"`.
- **Session end:** run the same command with `--clear-display-agent --clear-title`.
- **Codex:** skip until #48500 is fixed, unless Codex runs with `--no-daemon`. Document the sidebar row option
  instead.

## tmux

### How names and titles work

Source: tmux 3.8 man page, "NAMES AND TITLES".

- **Windows have names, and panes have titles.** A window's name shows in the status bar (`#W`). With
  `automatic-rename on` (the default), the name follows the active pane's command, like "claude" or "codex".
  - `rename-window` sets the name and turns `automatic-rename` off for that window.
  - Programs can rename a window only with `\ek…\e\\`, and only when `allow-rename` is on (it's off by default).
    Agents don't send that sequence.
- **A pane's title** (`#T`, `pane_title`) defaults to the hostname. Programs set it with OSC 0/2 when
  `allow-set-title` is on (the default), and `select-pane -T` sets it too. The default `status-right` shows
  `#{=21:pane_title}`, and `set-titles on` passes it to the outer terminal.
- **User options:** `set-option -p -t "$TMUX_PANE" @rostr "🧙 Gandalf"` stores a value that formats can show
  (`pane-border-format`, `window-status-format`, `status-right`). Nothing overwrites it.
- **Detection and targeting:** use `$TMUX` and `$TMUX_PANE`, and always pass `-t "$TMUX_PANE"`. Without `-t`,
  tmux commands act on the client's active pane, which may not be the agent's. A pane ID resolves to its window
  for window commands. Under nested tmux, `TMUX` and `TMUX_PANE` point at the innermost server, which is the
  right one.

### What the agents do

- **Claude Code** keeps writing an OSC title of a status glyph plus an auto-generated label. Since 2.1.236 it
  writes only when the text changes. A request to put the session name in the title was closed as not planned
  ([#55397](https://github.com/anthropics/claude-code/issues/55397), June 2026), and so was one for a
  configurable title ([#76092](https://github.com/anthropics/claude-code/issues/76092)).
  `CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1` turns this off
  ([env vars](https://code.claude.com/docs/en/env-vars)). So tmux shows Claude's label, **not** the rostr name.
  **Unverified:** whether a `/rename` or hook `sessionTitle` now feeds that label.
- **Claude Code hooks can write a title** with the `terminalSequence` output field (OSC 0/1/2 are allowed).
  Claude Code writes it to its own terminal, so it works in tmux without `/dev/tty`
  ([hooks: emit terminal notifications](https://code.claude.com/docs/en/hooks#emit-terminal-notifications)).
  But Claude's next title change overwrites it.
- **Codex** writes OSC titles from `tui.terminal_title`. Adding `thread-name` gets the rostr name into `#T`
  durably (see Codex above).
- **Hook environment:** Claude Code hooks inherit the agent's `TMUX_PANE`. Codex hooks under the daemon get a
  stale `TMUX_PANE` ([#48500](https://github.com/openai/codex/issues/48500)).

### What breaks

| Problem | Effect | Mitigation |
|---|---|---|
| Several agents in one window | `rename-window` is last-writer-wins | Rename the window only when it has one pane (`#{window_panes}` = 1). Otherwise use the pane option and border |
| Name sticks after the session ends | `rename-window` turned `automatic-rename` off | At session end, `set -w -t "$TMUX_PANE" automatic-rename on`, only if rostr renamed it (track with `@rostr_renamed`) |
| User already named the window | rostr would clobber it | Rename only if `#{automatic-rename}` is on |
| Agent overwrites the title | `select-pane -T` and OSC titles from rostr get replaced | Don't fight over the pane title. Use the window name or a user option |
| Codex daemon | Hook targets the wrong pane | No tmux calls from Codex hooks. Use the `thread-name` terminal title |
| Nested tmux, or herdr inside tmux | Outer vs. inner targeting | Innermost `TMUX_PANE` is correct. Under herdr (`HERDR_ENV=1`), skip tmux |
| Hook isn't in tmux (desktop app, SSH without tmux) | n/a | Do nothing unless `TMUX` and `TMUX_PANE` are both set |

### Recommended minimal tmux approach

For Claude Code only, when `TMUX` and `TMUX_PANE` are set and `HERDR_ENV` isn't:

1. **On draft, resume, or compact:** `tmux set -p -t "$TMUX_PANE" @rostr "<display>"`, which is always safe.
2. **Same events, if the window has one pane and `automatic-rename` is on:**
   `tmux rename-window -t "$TMUX_PANE" "<display>"` and `tmux set -w -t "$TMUX_PANE" @rostr_renamed 1`.
3. **On session end:** unset `@rostr`, and if `@rostr_renamed` is set, turn `automatic-rename` back on and unset
   the flag.
4. **Document** `#{@rostr}` for `pane-border-format` for people who split windows.

Codex gets no tmux code. It relies on `tui.terminal_title` with `thread-name`.

## Open questions and things to verify hands-on

**Codex**

- Does `codex app-server proxy` take plain JSON-RPC lines, or does the client have to speak WebSocket? What is
  the default control-socket path, and can a hook reach it?
- Does `thread/name/set` through the daemon update a live TUI's `thread-name` title, and the ChatGPT desktop app
  and IDE extension sidebars?
- Do the desktop app and IDE extension run `~/.codex/hooks.json` hooks at all, and do they show the `/hooks`
  trust prompt?
- Does Codex's auto-namer overwrite a name rostr set? Is there a race on the first turn?
- Are hook commands sandboxed?
- Can the agent's own `rostr agent task` write `~/.config/rostr` or reach the API under the default
  `workspace-write` sandbox? If not, does rostr need `sandbox_workspace_write.writable_roots` and
  `network_access`, or should Codex task updates flow through a hook instead?
- Does `/clear` fire `SessionEnd` for the old thread?
- When does `SessionStart` `startup` fire relative to the first prompt?
- Is `session_id` always equal to `CODEX_THREAD_ID`, including for subagents? (Subagent hooks use the parent's
  ID.)
- Do Codex Cloud tasks run repo hooks?
- Will #44902 or #48500 ship a per-client env allowlist, so herdr and tmux targeting can come back?

**Claude Code**

- In tmux, does `/rename` or hook `sessionTitle` change Claude's OSC title (`#{pane_title}`)?
- Does `CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1` now stop ongoing title writes? (#47397 said it didn't.)

**herdr**

- Does `--display-agent` render emoji cleanly in the sidebar?
- Does `--agent claude` stop the label from lingering after Claude exits?
- Is `TMUX` unset inside herdr panes when herdr runs under tmux?

**tmux**

- Confirm that `#{@rostr}` in `window-status-format` resolves to the active pane's option.
