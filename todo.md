# Todo

## CLI design

Decisions live in [`docs/cli-spec.md`](docs/cli-spec.md).

- [ ] Work through the findings in [`cli-review.md`](cli-review.md), writing each decision into the spec, then delete
      that file.

## Deferred decisions

Decide once there's a working build to play with.

- [ ] How the first-prompt draft step works in terminal sessions: whether to wake Claude there too (one extra silent
      model turn) so its summary becomes the task and title, or keep the start of the prompt.

## Project

- [x] Pick a license (MIT).
- [ ] Finalize the A team and B team for each preset roster.
- [x] Make a few more pre-built rosters (added NBA, MLB, NFL, and NHL Legends).
- [ ] Design pass on the /rosters page.
- [ ] Logo and iconography design: the logo, the favicon (the site still uses Astro's default), and icons for the site.
- [ ] General copy pass on everything on the website.
- [ ] Upgrade the home page's "Works out of the box with Claude Code and Codex" line to say where: the Claude Code and
      Codex desktop apps, the terminal, tmux, and herdr.
- [ ] Human pass on the "Give your poor human brain a rest" section copy. It's not doing the trick yet.
- [x] Write the agent setup instructions (`site/public/setup.md`, linked from the home page and `llms.txt`).

## Legal

- [ ] Terms of service and a privacy policy before launching the cloud API (the /privacy page explains the design but
      isn't a formal policy).
- [ ] Move rostr under Rosebud Labs: the `LICENSE` copyright, the getrostr.sh domain, and the Vercel, Railway, and
      Cloudflare accounts.

## CLI build

Build from [`docs/cli-spec.md`](docs/cli-spec.md).

- [ ] Go CLI for macOS and Linux.
- [ ] Install paths that `setup.md` and the site promise: GitHub releases of the binary, the `piazzatron/homebrew-tap`
      formula, and the `getrostr.sh/install` script.
- [ ] Roster commands: `assemble` (with the wizard and the hidden `create` alias), `link` / `unlink`, `list`, `edit`,
      `presets`, `disband`.
- [ ] Agent commands: `agent draft`, `agent release`, `agent task`.
- [ ] `config`, including the `cloud on` migration.
- [ ] Local storage ([`docs/local-storage.md`](docs/local-storage.md)).
- [ ] Server API and Postgres schema ([`docs/server-schema.md`](docs/server-schema.md)), with the HKDF-derived keys,
      task encryption, and repo fingerprints.
- [ ] The hidden `rostr hook claude-code …` command, the installed hooks that call it, the skill, and `/draft`.
- [ ] Detect headless `claude -p` runs in the hooks (and Codex's equivalent) and skip drafting for them.
- [ ] Check what Codex's hooks provide (a session ID at start and end, a way to set env vars), then build its hooks
      and skill.
- [ ] Test release and resume in local sessions and in cloud sessions.

## Monitoring

- [ ] Point Uptime Robot at `https://api.getrostr.sh/health` (`200` when the server and database are up, `503` when
      the database is unreachable), with alerts to Discord.
- [ ] Send CI failures to Discord too.
- [ ] Set Railway's healthcheck path to `/health` once the real server is deployed.

## Cloud access instructions

Write the setup steps for Claude Code on the web and Codex cloud:

- [ ] Put `ROSTR_KEY` in the cloud environment's variables (never in a committed file).
- [ ] Allow `api.getrostr.sh` in the environment's network settings.
- [ ] Install rostr in the environment's setup script (needs a Linux install path, e.g. `curl -fsSL https://getrostr.sh/install | sh`).
- [ ] Commit the hooks to the repo's `.claude/settings.json`, since personal settings don't reach cloud containers.

## GitHub

- [ ] Make the repo public so the site's "Submit your own" link works.
- [ ] Polish the README for launch.
- [ ] Add a contributing guide (`CONTRIBUTING.md`, linked from the README).
- [ ] Add an issue template and a `roster-submission` label for submitted rosters.

## Integrations

- [ ] Confirm herdr support, or make a plugin for it.

## To Test

- [ ] Test that agent-based installs work properly.

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
