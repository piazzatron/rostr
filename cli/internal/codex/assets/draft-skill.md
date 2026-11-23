---
name: rostr-draft
description: Draft a rostr name for this session. Use only when the user invokes $rostr-draft or asks for a rostr name.
---

# Draft a rostr name

Run this command:

```sh
rostr hook codex draft
```

If the user named someone, with or without an emoji, pass the name, quoted: `rostr hook codex draft '🦊 Fox'`. That
drafts them, adding them to the roster if they aren't in it yet.

Follow what it prints, then tell the user who you are in one short line.
