# Set up rostr

You're an AI coding agent, and your user asked you to set up rostr. These are your instructions. Follow them in
order. rostr gives every coding-agent session a name from a roster (like 🧙 Gandalf). Full docs:
https://getrostr.sh/cli

If the user asked you to remove rostr instead, skip to [Uninstall rostr](#uninstall-rostr). rostr runs on macOS and
Linux only; on anything else (including Windows outside WSL), stop and tell the user.

## 1. Install rostr

Run `rostr --version`. If it's already installed, run `rostr stats`, and if the user already has rosters, ask whether
they want another one before going on. If it isn't installed, use Homebrew if `brew` is available:

```sh
brew install piazzatron/tap/rostr
```

Otherwise:

```sh
curl -fsSL https://getrostr.sh/install | sh
```

If the script says the directory it installed to isn't on the PATH, ask the user whether to add the `export PATH=…`
line it printed to their shell profile (rostr's hooks only work when `rostr` is on the PATH), and run that line in
your own shell too. Then run `rostr --version` again to confirm it worked.

## 2. Ask whether they already use rostr

Ask the user whether they already use rostr in the cloud on another machine.

If they do, this machine joins their setup with their rostr key, and the key must never pass through you. Ask them to
run this themselves in a terminal, pick "I have a key", and paste their key (`rostr config key` on their other machine
prints it):

```sh
rostr cloud on
```

That also installs rostr's hooks here. Once they say it's done, run `rostr stats` to check their rosters are there,
then skip to [step 4](#4-draft-yourself).

## 3. Ask which roster, then assemble it

`rostr assemble` is an interactive wizard you can't drive, so ask the user its questions yourself, then pass the
answers as flags. How you ask depends on where the user sees your questions, so first run
`echo "$CLAUDE_CODE_ENTRYPOINT"`:

- **It prints nothing (Codex, or another agent that isn't Claude Code):** ask in plain text, not with a question
  tool, one question at a time. First, which roster: run `rostr presets` and write out every roster it prints as a
  numbered list in your message, one line each (emoji, name, source), leaving none out: the user can't see command
  output, so don't summarize it. End the list with one more line, "Your own names", saying they can type their own
  names instead, each optionally with an emoji (`🧮 Ada, Grace, Linus`), drafted in the order they list them. Then
  link https://getrostr.sh/rosters for each roster's members, and ask them to reply with a number, a roster's name,
  or their own names. Once they've answered, ask whether to use the defaults below, showing the
  table.
- **It prints anything else (Claude Code):** ask both of the questions below in one go, with your question tool.

1. **Which roster?** Keep the question itself short: "Which roster? See them all at https://getrostr.sh/rosters".
   - **`cli` (Claude Code in a terminal):** offer exactly four options: The Fellowship, Hogwarts, Free Agents (a
     random mix from every roster), and last, "Something else". Give each roster a preview of its first 16 members,
     one per line, from this command (don't read `rostr presets --json` yourself: it's long):

     ```sh
     rostr presets --json | jq -r '.presets[] | select(.id == "fellowship" or .id == "hogwarts" or .id == "free-agents") | .id + ": " + ([.members[:16][] | .emoji + " " + .name] | join(", "))'
     ```

     The terminal has no free-text option alongside previews, so "Something else"'s preview starts with how to use
     it: press `n` and type any roster below, or their own names, each optionally with an emoji
     (`🧮 Ada, Grace, Linus`), drafted in the order they list them. Below that, list every roster `rostr presets`
     prints, one line each (emoji and name), leaving none out. If they pick "Something else" without typing anything,
     ask them in plain text what they want.
   - **Anything else (the desktop app):** offer exactly three options, with no previews: The Fellowship,
     Hogwarts, and Free Agents (a random mix from every roster). Your question tool adds its own free-text option
     last. End Free Agents' description by saying they can type any roster from the rosters page there instead, or
     their own names, each optionally with an emoji (`🧮 Ada, Grace, Linus`), drafted in the order they list them.
2. **Use the defaults?** List the defaults below, with two options: use them, or change some.

| Setting | Default | Other choices |
| --- | --- | --- |
| Where | The cloud: every machine and cloud environment shares the roster, with task and repo names end-to-end encrypted | This machine only (leave out `--cloud`) |
| Emoji | On (🧙 Gandalf) | `--emoji off` (Gandalf), `--emoji only` (🧙) |
| Naming | Every new session, automatically | Only when they type `/draft` (`--no-auto-draft`) |
| Team size | 16, the top of the roster's list | `--size 4`, `--size 8`, `--size all` |

If they want to change some, ask all four settings in one follow-up, one question each, with the default as the first
option.

Then run `rostr assemble` with their answers. With the defaults:

```sh
rostr assemble --preset <id> --cloud
```

For their own names, use `rostr assemble <name> --names "<comma-separated names>"` instead of `--preset`, with a short
name you suggest (like `work`) and let them change. Don't ask for a name for a pre-built roster: rostr names it after
its id.

This also installs rostr's hooks, skill, and `/draft` command for Claude Code (and, if Codex is installed, its hooks
and skills). Don't edit `~/.claude/settings.json` or other agent config by hand.

## 4. Draft yourself

This session started before rostr's hooks were installed, so draft your own name now. In Claude Code, run:

```sh
rostr hook claude-code draft --session "$CLAUDE_CODE_SESSION_ID"
```

In Codex, run `rostr hook codex draft` instead. Follow what it prints (it tells you your name, and may ask you to
title the session). Then run the `rostr task` command it gives you, with "set up rostr" as the task: in a terminal,
that's what puts your name in the session's title, from the user's next message on. Then tell the user who you are in
one short line. If it says it doesn't know the session's ID, ask the user to type `/draft` (or `$rostr-draft` in
Codex) instead.

## 5. Tell the user what's next

- **Their lineup:** every member `rostr assemble` printed, with their emoji, introduced simply (like "Your Westeros
  lineup:"). The user can't see command output, so list them all in your message rather than summarizing.
- Every new session gets a name on its own from now on.
- **If they chose the cloud:** to use rostr on another machine, they run `rostr cloud on` there and paste their key.
  For cloud environments, they add the key as `ROSTR_KEY` in each environment's variables. Either way, they get the
  key by running `rostr config key` themselves in a terminal. Never run `rostr config key` yourself, and never read
  `~/.config/rostr/credentials` or `ROSTR_KEY`: the key would end up in your transcript.

If any command fails, show the user the error rather than working around it.

## Uninstall rostr

Only when the user asked you to remove rostr. It deletes all their rosters, and in the cloud their whole cloud setup,
which can't be undone, so confirm with them first. Then run `rostr uninstall --yes`, and run the commands its output
ends with to remove the program itself. If the user had set `ROSTR_KEY` in their shell profile or a cloud environment's
variables, tell them to remove it.
