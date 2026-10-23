# Todo

## Plan to launch

Get everything working end to end, then a final pass over the landing page.

1. [ ] **Solid Claude Code experience** (Michael, hands-on): faster drafting and titles, the terminal title decision,
   and the spec's "Verify" items (compaction keeps the name, resume and `/clear`, session end fires, no permission
   prompts).
2. [x] **Real install paths** (agent): GoReleaser releases for macOS and Linux, the `piazzatron/homebrew-tap` formula,
   and the `getrostr.sh/install` script. The repo goes public.
3. [ ] **Cloud in a real cloud session**: rostr in Claude Code on the web against production (`ROSTR_KEY`, network
   allowlist, setup-script install, hooks committed to the repo, `origin` behind the proxy). Becomes the cloud access
   instructions.
   - [ ] Cloud environments cache the setup script's result ("Setup script cached from previous run"), so a new rostr
         release doesn't reach an environment until the cache is invalidated. Per the docs, it's rebuilt when the
         setup script or allowed network hosts change, or after about 7 days. Decide how users pick up new releases
         (tell them to edit the script, or have rostr update itself in the background from a SessionStart hook).
4. [x] **Codex** (agent): `rostr hook codex …`, a skill-based `/draft`, thread renaming.
5. [x] **tmux and herdr** (agent): pane tag and window name in tmux; `herdr pane report-metadata` in herdr.
6. [ ] **Cleanup**: review findings N10 and N11 (then delete `cli-review.md`), Uptime Robot with Discord alerts.
7. [ ] **Final landing page pass.**

## CLI design

Decisions live in [`docs/cli-spec.md`](docs/cli-spec.md).

- [ ] Work through the findings in [`cli-review.md`](cli-review.md), writing each decision into the spec, then delete
      that file.
- [x] Get rostr API call latency off the first prompt: hooks draft from a local cache and confirm in the background
      (see [`docs/drafting-latency.md`](docs/drafting-latency.md)).
- [ ] Measure the API's latency from a Claude Code cloud container (the one place a first prompt still waits on it),
      and `rostr list`'s several calls (~1.4s from Tokyo).
- [ ] Real first prompts are long tasks, so the agent sets its `name · summary` title late: after a long plain answer,
      or behind slow first tool calls. Local sessions show no name until then. Think about how to get the name up
      sooner.
- [ ] An easier way to see, from inside a session, what the whole roster is doing (today it's asking the agent to run
      `rostr list`).

## Deferred decisions

Decide once there's a working build to play with.

- [x] How the first-prompt draft step works in terminal sessions: the title is the start of the prompt right away,
      then `name · task` from the next prompt.

## Project

- [x] Pick a license (MIT).
- [ ] Finalize the A team and B team for each preset roster.
- [x] Make a few more pre-built rosters (added NBA, MLB, NFL, and NHL Legends).
- [ ] Design pass on the /rosters page.
- [x] Logo and iconography design: the logo, the favicon (the site still uses Astro's default), and icons for the site.
- [ ] General copy pass on everything on the website.
- [ ] Decide the final tagline ("Give your agents names and remember who's doing what"), used in the CLI's help and on
      the website.
- [x] A "How it works" section on the site, linking to a write-up of how rostr names sessions (hooks, the app's own
      titles, Remote Control vs Local vs terminal, the title cache) from `docs/claude-code-session-titles.md`.
- [ ] Do a pass on the How it works page; it reads way too Claude-y.
- [x] A latency and performance section on the site, once it's really fast: how quickly the name shows up, and how.
      It matters a lot.
- [ ] Upgrade the home page's "Works out of the box with Claude Code and Codex" line to say where: the Claude Code and
      Codex desktop apps, the terminal, tmux, and herdr.
- [x] Human pass on the "Give your poor human brain a rest" section copy. It's not doing the trick yet.
- [x] Write the agent setup instructions (`site/public/setup.md`, linked from the home page and `llms.txt`).

## Legal

- [x] Terms of service and a privacy policy before launching the cloud API (the /privacy page explains the design but
      isn't a formal policy).
- [ ] Move rostr under Rosebud Labs: the getrostr.sh domain, and the Vercel, Railway, and Cloudflare accounts (the
      `LICENSE` copyright is done).

## CLI build

Build from [`docs/cli-spec.md`](docs/cli-spec.md).

- [x] Go CLI for macOS and Linux.
- [x] Install paths that `setup.md` and the site promise: GitHub releases of the binary, the `piazzatron/homebrew-tap`
      formula, and the `getrostr.sh/install` script.
- [x] Roster commands: `assemble` (with the wizard and the hidden `create` alias), `link` / `unlink`, `list`, `edit`,
      `presets`, `disband`.
- [x] Agent commands: `draft`, `bench`, `task`.
- [x] `config`, including the `cloud on` migration.
- [x] Local storage ([`docs/local-storage.md`](docs/local-storage.md)).
- [x] Server API and Postgres schema ([`docs/server-schema.md`](docs/server-schema.md)), with the HKDF-derived keys,
      task encryption, and repo fingerprints.
- [x] The hidden `rostr hook claude-code …` command, the installed hooks that call it, the skill, and `/draft`.
- [x] Make drafting and the title summary much faster: drafting happens before the first reply, and the summary
      comes when the first turn finishes.
- [ ] Understand how Homebrew releases work (the tap, the formula GoReleaser pushes, `brew upgrade`); see
      `RELEASING.md`.
- [x] Upgrading rostr refreshes the installed hooks: a session start that finds them out of date reinstalls them in
      the background (`docs/cli-spec.md`, Upgrading).
- [x] Upgrading rostr refreshes the skill and /draft text too (a /draft of the user's own is left alone).
- [x] A Remote Control host's own first session has no `claude-code-remote` title tool (only sessions started from the
      app or claude.ai get it); the instruction now tells the agent to skip titling without it.
- [ ] Hook titles don't reach the app or claude.ai in any Remote Control session, even on Claude Code 2.1.308:
      anthropics/claude-code#104473 (the app and the `remote-control` host both ignore the engine's title report).
- [x] Archived desktop and cloud sessions never fire SessionEnd, so their names stayed drafted (♟️ Ron, Gimli, Merry).
      Sessions now rest 15 minutes after their turn ends, and a draft that finds no free name takes the longest-resting
      one before handing out copies (`docs/cli-spec.md`, Resting).
- [x] Check that the `Stop` hook fires in the Mac desktop app: it does in Local sessions (🌱 Samwise, Oct 23), except
      after a first turn the title hook ends, which records the turn's end itself.
- [x] Detect headless `claude -p` runs in the hooks and skip drafting for them.
- [ ] Do the same for Codex's `codex exec` (check what its hooks can see).
- [x] Check what Codex's hooks provide (a session ID at start and end, a way to set env vars), then build its hooks
      and skill.
- [ ] Test release and resume in local sessions and in cloud sessions.

## Monitoring

- [ ] Point Uptime Robot at `https://api.getrostr.sh/health` (`200` when the server and database are up, `503` when
      the database is unreachable), with alerts to Discord.
- [ ] Send CI failures to Discord too.
- [ ] Set Railway's healthcheck path to `/health` once the real server is deployed.

## Cloud access instructions

Write the setup steps for Claude Code on the web and Codex cloud:

- [ ] Give explicit, clear instructions for setting rostr up in cloud environments, on the site and in `setup.md`.
- [x] Put `ROSTR_KEY` in the cloud environment's variables (never in a committed file).
- [x] Allow `api.getrostr.sh` in the environment's network settings.
- [x] Install rostr in the environment's setup script: `curl -fsSL https://getrostr.sh/install | sh`.
- [x] ~~Commit the hooks to the repo's `.claude/settings.json`~~: superseded, since the install script runs
      `rostr cloud install` when `CLAUDE_CODE_REMOTE` is set.

## GitHub

- [x] Make the repo public so the site's "Submit your own" link works.
- [x] Polish the README for launch.
- [x] Add a contributing guide (`CONTRIBUTING.md`, linked from the README).
- [x] Add an issue template and a `roster-submission` label for submitted rosters.

## Integrations

- [ ] Confirm herdr support, or make a plugin for it.

## To Test

- [ ] Test that agent-based installs work properly.
- [ ] Codex: approve the hooks in `/hooks`; on the first prompt the agent knows its name and the thread shows
      `🧙 Gandalf · …` in the TUI, `codex resume`, and the app (does Codex's own auto-naming overwrite it?).
- [ ] Codex: a fork gets a new name; `/compact` and `resume` re-tell the same one; quitting releases within 3s; does
      `/clear` fire SessionEnd?
- [ ] Codex sandbox: can `$rostr-draft` and `rostr task` write `~/.config/rostr` and reach the daemon socket under
      the default `workspace-write` sandbox?
- [ ] Codex terminal title: with `tui.terminal_title = ["spinner", "thread-name", "project"]` the name reaches tmux and
      herdr.
- [ ] tmux (Claude Code): a single-pane window takes the name and `#{@rostr}` is set, both reverted on exit; a window
      you named, or a split one, isn't renamed.
- [ ] herdr (Claude Code): the sidebar shows `🧙 Gandalf` instead of "claude", the emoji renders cleanly, and it
      clears on exit.

## Testing

- [ ] Make sure the permissions story works well in Claude Code: the hooks, the agent's `rostr` commands, the session
      title tool, and `/draft` shouldn't leave users clicking through permission prompts.
- [ ] Claude Code in the terminal
- [ ] Claude Code in the app
- [ ] Codex in the terminal
- [ ] Codex in the app

## Future roadmap

- [ ] Windows support (not in v1).
- [ ] Team rosters: share a roster with teammates (would need a team key on top of per-person keys).
