---
description: Draft a rostr name for this session, or the one you name
argument-hint: "[<emoji> <name>]"
allowed-tools: Bash(rostr hook claude-code draft:*)
---

!`rostr hook claude-code draft --session ${CLAUDE_SESSION_ID} -- $ARGUMENTS`

Follow the instructions above, then tell the user who you are in one short line.
