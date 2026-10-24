# Set up rostr

You're an AI coding agent, and your user asked you to set up rostr. These are your instructions. Follow them in
order.

rostr gives every coding-agent session a name from a roster the user picks (like 🧙 Gandalf from The Fellowship), so
they can tell their agents apart. It's a CLI plus hooks, a skill, and a `/draft` slash command. Full docs:
https://getrostr.sh/cli

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

Then run `rostr --version` again to confirm it worked. If it already was installed, run `rostr list`: if the user
already has rosters, ask whether they want another one before going on.

## 3. Ask the user these questions

`rostr assemble` is an interactive wizard you can't drive, so ask the user its questions yourself, then pass the
answers as flags. Ask all of them, in one go if you can, using your question tool if you have one. Offer the defaults.

1. **Which roster?** Run `rostr presets` and offer a handful, for example The Fellowship, Hogwarts, Smash Bros, NBA
   Legends, The Office, or Free Agents (a random mix). Or they can use their own list of names, each optionally with
   an emoji (`🧮 Ada, Grace, Linus`), drafted in the order they list them.
2. **How big a team?** Only for a pre-built roster: 4, 8, 16 (the default), or everyone on it. Their team is that many
   from the top of the roster's list (see https://getrostr.sh/rosters), and they can change members later with
   `rostr edit`.
3. **What should the roster be called?** Default: `work`. People often keep one roster for work and another for side
   projects.
4. **Emoji?** `on` (🧙 Gandalf, the default), `off` (Gandalf), or `only` (🧙).
5. **Where should rostr live?** On this machine (the default; nothing leaves it), or in the cloud, so agents in cloud
   environments (Claude Code on the web, Codex cloud) share the same roster. Task and repo names are end-to-end
   encrypted in the cloud.
6. **Name every new session automatically?** Yes (the default), or only when they type `/draft`.

## 4. Assemble the roster

Run `rostr assemble` with their answers:

```sh
rostr assemble <name> --preset <id> --size <4|8|16|all> --emoji <on|off|only>
```

- Use `--names "<comma-separated names>"` instead of `--preset` and `--size` for their own names.
- Add `--cloud` if they chose the cloud.
- Add `--no-auto-draft` if they don't want every session named automatically.

This also installs rostr's hooks, skill, and `/draft` command for Claude Code (and, if Codex is installed, its hooks
plus the `rostr` and `$rostr-draft` skills for Codex).
Don't edit `~/.claude/settings.json` or other agent config by hand.

## 5. Check it worked

```sh
rostr stats
```

It should show the new roster with everyone on the bench.

## 6. Tell the user what's next

- Names start with their **next new session**: hooks load when a session starts, so this one stays unnamed.
- **If they use Codex:** Codex only runs hooks the user has approved, so they need to open `/hooks` in Codex and
  trust rostr's. In Codex, `$rostr-draft` takes the place of `/draft`. To see the name in tmux or herdr, they can set
  `tui.terminal_title = ["spinner", "thread-name", "project"]` in `~/.codex/config.toml`. If they run
  `codex --no-daemon` (or set `daemon_auto_start = false` under `[features]`), rostr labels the pane itself instead.
- To give a project its own roster, they can run `rostr assemble` again for a second roster, then `rostr link` in
  that project.
- **If they chose the cloud:** to use rostr in cloud environments, they should run `rostr config key` themselves in
  a terminal and add the key as `ROSTR_KEY` in each cloud environment's variables. Never run `rostr config key`
  yourself, and never read `~/.config/rostr/credentials` or `ROSTR_KEY`: the key would end up in your transcript.

If any command fails, show the user the error rather than working around it.
