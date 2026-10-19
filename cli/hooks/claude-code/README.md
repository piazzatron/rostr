# Claude Code session-title hook

Drafts an agent name for each new Claude Code session, tells Claude its name, and puts the name at the front
of the session title (for example `Otter · Vite Tailwind build error`).

- **Terminal sessions:** the title is set instantly by the hook.
- **Desktop / Remote Control sessions:** the app's title is set a few seconds after Claude's first reply,
  without blocking it.

Only a session's first prompt triggers it. Sessions that already have prompts are left alone.
See [docs/claude-code-session-titles.md](../../../docs/claude-code-session-titles.md) for why it works this way.

## Install

Add both entries to `~/.claude/settings.json`, replacing `/path/to/rostr` with your checkout, and allow the
rename tool so desktop sessions don't ask for permission:

```json
{
  "permissions": {
    "allow": ["mcp__claude-code-remote__set_session_title"]
  },
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "python3 /path/to/rostr/cli/hooks/claude-code/session_title.py name"
          },
          {
            "type": "command",
            "asyncRewake": true,
            "command": "python3 /path/to/rostr/cli/hooks/claude-code/session_title.py rename"
          }
        ]
      }
    ]
  }
}
```

Merge these into any `permissions` and `hooks` you already have instead of replacing them.

## Known limits

- The desktop rename depends on Claude following an instruction, so it isn't guaranteed.
- It reads `~/.claude/sessions/*.json` to find the server session ID. That file format is undocumented and
  could change in any Claude Code release.
