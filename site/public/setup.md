# Set up rostr

You're an AI coding agent, and your user asked you to set up rostr. These are your instructions. Follow them in
order.

rostr gives every coding-agent session a name from a roster the user picks (like 🧙 Gandalf from The Fellowship), so
they can tell their agents apart. It's a CLI plus hooks, a skill, and a `/draft` slash command. Full docs:
https://getrostr.sh/cli

If the user asked you to remove rostr instead, skip to [Uninstall rostr](#uninstall-rostr).

## 1. Check the platform

rostr runs on macOS and Linux. On anything else (including Windows outside WSL), stop and tell the user rostr doesn't
support it yet.

## 2. Install rostr

Check whether it's already installed:

```sh
rostr --version
```

If it isn't, install it. Use Homebrew if `brew` is available:

```sh
brew install piazzatron/tap/rostr
```

Otherwise:

```sh
curl -fsSL https://getrostr.sh/install | sh
```

If the script says the directory it installed to isn't on the PATH, ask the user whether to add the `export PATH=…`
line it printed to their shell profile (rostr's hooks only work when `rostr` is on the PATH), and run that line in
your own shell too.

Then run `rostr --version` again to confirm it worked. If it already was installed, run `rostr stats`: if the user
already has rosters, ask whether they want another one before going on.

## 3. Ask whether they already use rostr

Ask the user whether they already use rostr in the cloud on another machine.

If they do, this machine joins their setup with their rostr key, and the key must never pass through you. Ask them to
run this themselves in a terminal, pick "I have a key", and paste their key (`rostr config key` on their other machine
prints it):

```sh
rostr cloud on
```

That also installs rostr's hooks here. Once they say it's done, run `rostr stats` to check their rosters are there,
then skip to [step 7](#7-draft-yourself).

## 4. Ask the user which roster, and whether the defaults are fine

`rostr assemble` is an interactive wizard you can't drive, so ask the user its questions yourself, then pass the
answers as flags. Ask both questions in one go, using your question tool if you have one.

First, show them every roster. Run `rostr presets --json` and list them all in your reply, one line each with its
emoji, name, and what it's from, and link https://getrostr.sh/rosters to see everyone on each one.

1. **Which roster?** Offer three or four of the rosters as options, plus Free Agents (a random mix from every
   roster). If your question tool can show a preview for each option, show the roster's first 16 members (the team
   they'd get). Say they can also name any other roster from the list, or use their own names, each optionally with
   an emoji (`🧮 Ada, Grace, Linus`), drafted in the order they list them.
2. **Use the defaults?** Yes, or change some. The defaults are:
   - **The cloud**, so every machine and cloud environment (Claude Code on the web, Codex cloud) shares the same
     roster. Task and repo names are end-to-end encrypted. The other choice is this machine only, where nothing leaves
     it.
   - **Emoji on** (🧙 Gandalf). The others are `off` (Gandalf) and `only` (🧙).
   - **Every new session named automatically.** The other choice is only when they type `/draft`.
   - **A team of 16**, the top of the roster's list. The others are 4, 8, or everyone on it. They can change members
     later with `rostr edit`.

Only if they want to change some, ask about the ones they want to change.

Don't ask for a roster name: rostr names a pre-built roster after its id (like `fellowship`). For their own names,
suggest a short name (like `work`) and let them change it.

## 5. Assemble the roster

Run `rostr assemble` with their answers. With the defaults:

```sh
rostr assemble --preset <id> --cloud
```

- Add `--size <4|8|all>` for a team size other than 16.
- Use `rostr assemble <name> --names "<comma-separated names>"` instead of `--preset` for their own names.
- Leave out `--cloud` if they want rostr on this machine only.
- Add `--emoji <off|only>` if they don't want emoji on.
- Add `--no-auto-draft` if they don't want every session named automatically.

This also installs rostr's hooks, skill, and `/draft` command for Claude Code (and, if Codex is installed, its hooks
plus the `rostr` and `$rostr-draft` skills for Codex).
Don't edit `~/.claude/settings.json` or other agent config by hand.

## 6. Check it worked

```sh
rostr stats
```

It should show the new roster with everyone on the bench.

## 7. Draft yourself

This session started before rostr's hooks were installed, so draft your own name now. In Claude Code, run:

```sh
rostr hook claude-code draft --session "$CLAUDE_CODE_SESSION_ID"
```

In Codex, run `rostr hook codex draft` instead. Follow what it prints (it tells you your name, and may ask you to
title the session), then tell the user who you are in one short line. If it says it doesn't know the session's ID,
ask the user to type `/draft` (or `$rostr-draft` in Codex) instead.

## 8. Tell the user what's next

- Every **new session** gets a name on its own from now on.
- **If they use Codex:** Codex only runs hooks the user has approved, so they need to open `/hooks` in Codex and
  trust rostr's. In Codex, `$rostr-draft` takes the place of `/draft`. rostr shows the name in tmux and herdr by finding the pane whose
  terminal title shows the Codex thread's name, so a custom `tui.terminal_title` in `~/.codex/config.toml` should keep
  `thread-name` in it.
- To give a project its own roster, they can run `rostr assemble` again for a second roster, then `rostr link` in
  that project.
- **If they chose the cloud:** to use rostr on another machine, they run `rostr cloud on` there and paste their key.
  To use it in cloud environments, they add the key as `ROSTR_KEY` in each environment's variables. Either way, they
  get the key by running `rostr config key` themselves in a terminal. Never run `rostr config key` yourself, and
  never read `~/.config/rostr/credentials` or `ROSTR_KEY`: the key would end up in your transcript.

If any command fails, show the user the error rather than working around it.

## Uninstall rostr

Only when the user asked you to remove rostr. It deletes all their rosters, and in the cloud it deletes their whole
cloud setup, which can't be undone, so confirm with them first. Then run:

```sh
rostr uninstall --yes
```

That takes rostr out of Claude Code and Codex and deletes its data, but leaves the rostr program installed. Its output
ends with the commands that remove the program (for a Homebrew install, `brew uninstall` and `brew untap`): run those
too, then check that `rostr --version` no longer finds it. If the user had set `ROSTR_KEY` in their shell profile or a
cloud environment's variables, tell them to remove it.
