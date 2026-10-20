# Todo

## CLI design

- [ ] Work through the open findings in [`cli-review.md`](cli-review.md) (#3–#15), then delete that file.
- [ ] `rostr use` is now `rostr link` / `rostr unlink` (documented), and setting the default roster moved to
      `rostr config default`. Still open: a way to see which roster the current project uses.
- [ ] Keep `create` as a hidden alias for `rostr assemble`, so people who guess `create` still get a roster.
- [ ] End-to-end encrypt tasks in cloud mode (decided). The CLI encrypts each task with AES-256-GCM using a key
      derived from the rostr key; the server stores only ciphertext; any machine with the key decrypts, so
      `rostr list` shows cloud tasks. The site's Privacy section and /privacy page describe this.
      - Trade-offs: raw API calls return encrypted tasks (dashboards go through `rostr list --json`), the server still
        sees task length and timing, and a future web dashboard or team rosters would need the key in the browser or
        shared.
- [ ] Derive separate keys from the rostr key with HKDF on the client: an auth token sent to the server (which stores
      only its hash), an encryption key for tasks, and a fingerprint key for repo names. The last two never leave the
      machine, which is what makes the Privacy section's claims true.
- [ ] Interactive `rostr assemble` wizard (documented): preset, roster name, emoji, local or cloud, auto-draft. The
      first run also installs hooks, skill, and slash commands. Agents run the same steps with flags.
- [ ] Auto-draft setting (`rostr config auto-draft on|off`, documented). Off means sessions start unnamed.
- [ ] Command groups (documented): roster commands at the top level, `rostr agent draft|release|task`, and
      `rostr config default|emoji|auto-draft|cloud|key`.
- [ ] Slash commands `/draft` and `/release`, installed by `rostr assemble`. Claude Code for now; add Codex's later.
- [ ] `rostr config emoji on|off|only` (documented). `only` shows just the emoji (e.g. `🧙 · Fix auth redirect loop`) and
      needs every member of a roster to have a different emoji.

## Project

- [x] Pick a license (MIT).
- [ ] Finalize the A team and B team for each preset roster.
- [ ] Make a few more pre-built rosters (the /rosters page shows them all; the home page shows the first 10).
- [ ] Design pass on the /rosters page.
- [ ] Logo and iconography design: the logo, the favicon (the site still uses Astro's default), and icons for the site.
- [ ] Human pass on the "Give your poor human brain a rest" section copy. It's not doing the trick yet.
- [ ] Write the agent setup instructions: what an agent reads and does when told "Set up rostr for me by following the
      instructions at https://getrostr.sh".

## Legal

- [ ] Terms of service and a privacy policy before launching the cloud API (the /privacy page explains the design but
      isn't a formal policy).
- [ ] Move rostr under Rosebud Labs: the `LICENSE` copyright, the getrostr.sh domain, and the Vercel, Railway, and
      Cloudflare accounts.

## CLI build

- [ ] One CLI in Go, built for macOS and Linux, used the same way by local and cloud sessions.

## Cloud auth

- [ ] One secret key per person, no account. `rostr config cloud on` gets a `rostr_sk_…` key from the server and saves it to
      `~/.config/rostr/credentials`. The key owns all of that person's rosters and project links.
- [ ] A machine is in cloud mode whenever it has a key (`credentials` or `ROSTR_KEY`); otherwise everything is local.
      No mixing local and cloud rosters.
- [ ] `rostr config key` prints the key. No rotation in v1.
- [ ] Server stores only a hash of each auth token (never the rostr key) and rate-limits key creation by IP.
- [ ] No recovery in v1: a lost key means lost cloud rosters.

## Session-start hook

- [ ] Read `session_id` from the hook's JSON input and run `rostr agent draft --session <id>`.
- [ ] Write `export ROSTR_SESSION=<id>` to `CLAUDE_ENV_FILE` so rostr commands the agent runs know their session.
- [ ] Set the agent's first task from the first prompt (`rostr agent task --session <id> "<summary>"`).
- [ ] Check what Codex's hooks provide: a session ID on start and end, and a way to set env vars for the agent's shell.
- [ ] Tell the agent to sign anything it sends you outside the session (Slack messages, emails, PR comments, etc.)
      with its name, so you know which agent posted it.

## Release hook

- [ ] Build the release hook: free the agent's name when the session ends. Use `SessionEnd`, not `Stop` (`Stop` fires
      after every reply). Find Codex's equivalent.
- [ ] Test it in local sessions and in cloud sessions.
- [ ] Server-side expiry as a backstop (24–48h) for sessions that die without running the hook.

## Resuming sessions

- [ ] A resumed session gets its old name back if it's still free. Draft by session ID so the same session always
      asks for the same name.

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

## Testing

- [ ] Claude Code in the terminal
- [ ] Claude Code in the app
- [ ] Codex in the terminal
- [ ] Codex in the app

## Future roadmap

- [ ] Windows support (not in v1).
- [ ] Team rosters: share a roster with teammates (would need a team key on top of per-person keys).
