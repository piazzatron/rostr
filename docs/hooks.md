# Hooks

How rostr plugs into Claude Code and Codex. Installed hooks are one-liners,
`if command -v rostr >/dev/null; then rostr hook <agent> <event>; fi`, so the logic and the text the agent is told
live in the binary and change with an upgrade, and a repo's committed hooks do nothing for teammates without rostr.
Each handler reads the hook's JSON from stdin and prints the reply the agent expects. Handlers are in
`cli/internal/app/hook.go` and `hook_codex.go`; the installers are `cli/internal/claudecode/install.go` and
`cli/internal/codex/install.go`.

## Claude Code

| Event | Handler | What it does |
|---|---|---|
| `SessionStart` | `session-start` | Writes `ROSTR_SESSION`. `startup`/`clear`: in cloud mode, drafts ahead in the background (see [drafting.md](drafting.md#drafting-ahead)). `resume`: drafts, asking for the old name back. `compact`: re-tells the agent its name (below) |
| `UserPromptSubmit` | `prompt` | First prompt: the draft step (below). Later prompts: come back from a rest, keep the draft alive, retitle the session to `<name> · <task>` if it differs (see [keepalive.md](keepalive.md)) |
| `PostToolUse`, title tools and `Bash` | `title-set` | Records the title's summary as the task, and ends a plain-answer turn (below) |
| `PostToolUse`, any tool | `working` | Reports that a turn started, if a turn end is pending: a turn can start without a prompt |
| `Stop` | `turn-end` | Records that the turn ended |
| `SessionEnd` | `session-end` | Benches the session's member and clears its files and pane label |

`/draft` runs `rostr hook claude-code draft`, which drafts even with auto-draft off. With auto-draft off, only the
first-prompt draft step is skipped. Headless runs (`claude -p`, detected from the parent process's arguments, since
neither the hook input nor the entrypoint tells them apart) are never drafted or re-told; Remote Control's own
`--print --sdk-url` sessions are not headless.

### The draft step and titles

Claude Code keeps two titles: a local one in the transcript, which hooks can set (`sessionTitle`), and the app's
server-side title, which hooks can set for desktop Local sessions but not for Remote Control or cloud sessions. So:

1. The first-prompt hook drafts, tells the agent who it is, and sets the title to `<name> · <start of the prompt>`, so
   the name shows straight away.
2. The agent is asked to title the session `<name> · <3-5 word summary>` with the app's title tool: in Local
   sessions after writing its answer, in Remote Control and cloud sessions before writing anything (text just before
   a tool call can be shown as a progress-update summary there, and the answer would appear twice).
3. Terminal sessions have no title tool, so the hook starts `rostr hook summarize` in the background: a headless
   `claude -p --model haiku` run that summarizes the prompt into the task, which the next prompt puts in the title.
4. When the agent has written its answer and every tool call since was titling, `title-set` returns
   `continue: false` to end the turn; otherwise Claude would reply to the tool result and repeat itself.

### Compaction and identity

After compaction the agent has lost its name, so the hook tells it again, but never gives the session a different
member: it re-tells the active draft, or reclaims the last member if free, or says nothing. The identity text
(`claudecode.Identity`) goes in the hook output rather than the skill, since skills load only when a conversation
looks relevant. It asks the agent to sign outside messages with its name, keep the name out of code and commits, and
run `rostr task` when its work changes.

## Codex

Same shape, with Codex's limits (`hook_codex.go`):

- Codex hooks can't set env vars or titles, and have no `asyncRewake`. The first prompt drafts synchronously, and
  rostr names the thread itself through the Codex app-server (`codex app-server proxy`, `thread/name/set`, 2-second
  limit; no daemon, no name).
- `SessionStart` fires only when the first prompt is sent, so there's nothing to gain from drafting ahead. A `fork`
  gets its own member.
- A "first prompt" is one whose rollout has no earlier `user_message`, so threads from before rostr aren't drafted
  mid-conversation.
- `/draft` is the `$rostr-draft` skill (Codex removed custom prompts). New hooks need approving in Codex's `/hooks`.
- Codex runs hooks in a shared app-server daemon whose pane variables belong to whichever terminal started it, so a
  hook trusts `HERDR_PANE_ID` and `TMUX_PANE` only when the nearest `codex` process above it is the TUI, not an
  `app-server` (`codex --no-daemon`). Otherwise it finds its pane by title (see below).

## tmux and herdr

Best-effort with a 1-second limit per command (`cli/internal/pane`). Whenever the agent is drafted, re-told, or
renamed, rostr reports the display name to herdr (`herdr pane report-metadata --agent <claude|codex>
--display-agent`), or in tmux sets the pane option `@rostr` and renames a single-pane window. `SessionEnd` undoes
both.

A Codex hook run by the daemon doesn't know its pane, and the app-server protocol doesn't tie threads to clients. But
rostr has just named the thread, and Codex's default terminal title is `<thread name> | <project>`. So rostr labels
the one Codex pane in `herdr pane list` (else `tmux list-panes -a`) whose title shows the name as a whole word,
retrying for up to a second while the title catches up. No match, or more than one, and it labels nothing.
`SessionEnd` clears the pane labeled with the released name. A custom `tui.terminal_title` needs `thread-name`.

## Upgrading

Each install records a fingerprint of what it wrote in `~/.cache/rostr/hooks/<agent>`. Every session start compares
it with the binary's own, and if it differs, runs `rostr hook sync` in the background, which reinstalls for each agent
that already has rostr's hooks. So after `brew upgrade` the first session brings everything up to date. Claude Code's
install replaces outdated rostr entries; Codex's only adds missing ones.
