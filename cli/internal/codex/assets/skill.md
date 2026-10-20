---
name: rostr
description: Reference for rostr, which gives each coding-agent session a name drafted from a roster (like 🧙 Gandalf) and puts it in the session title. Use when the user asks about rostr, who else is running or what other agents are working on, changing this session's task, setting rostr up, or adding, editing, linking, or disbanding rosters.
---

# rostr

rostr drafts a name for every coding-agent session from a roster, puts it at the front of the session title, and
frees it when the session ends. Hooks do the drafting and releasing, and they tell you your name, so you don't need
this skill to know who you are.

You run commands without a terminal, so rostr never prompts you: pass flags. Commands that need a person (`rostr edit`,
the `rostr assemble` wizard) error out, and you should ask the user to run them.

## Who's running

- `rostr list`: every roster, its drafted agents (name, task, repo, time since drafted), then its idle members.
- `rostr list <roster>... --json`: the same as JSON.

## Your task

- `rostr agent task "<task>"`: set what you're working on, in 3-5 words. Run it whenever your work changes.
- Your session comes from `CODEX_THREAD_ID`, so don't pass `--session`.

## Setting rostr up

- `rostr presets`: the pre-built rosters (id, emoji, name, source).
- `rostr assemble <name> --preset <id>`, or `rostr assemble <name> --names "🧮 Ada, Grace, ..."` (the first six are the
  A team; names without an emoji get one). Add `--emoji on|off|only`, and `--no-auto-draft` on the first run to draft
  only with `$rostr-draft`.
- Ask the user which preset or names they want before assembling.

## Rosters and projects

- `rostr link <roster>`: use a roster for this repo (a repo is its `origin`, as `owner/repo`). `rostr unlink` goes
  back to the default. `--repo owner/repo` targets another repo. Bare `rostr link` says which roster the repo uses.
- `rostr config`: every setting. `rostr config default <roster>`, `rostr config emoji on|off|only`,
  `rostr config auto-draft on|off`, `rostr config expiry 7d`.
- `rostr edit <roster>` opens the user's editor: ask them to run it.
- `rostr disband <roster> --yes` deletes a roster. Only run it when the user explicitly asks.
- `rostr agent release <name>` frees a name held by a session that died.

## Rules

- Never read `~/.config/rostr/credentials` or the `ROSTR_KEY` environment variable, and never run `rostr config key`.
- Don't put your rostr name in code, commit messages, or files.
