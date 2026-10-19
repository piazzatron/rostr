# Todo

## CLI design

- [ ] Work through the open findings in [`cli-review.md`](cli-review.md) (#3–#15), then delete that file.
- [ ] Emoji-only mode: show just the emoji, no names (e.g. `🧙 · Fix auth redirect loop`). Needs every member of a
      roster to have a different emoji.

## Project

- [x] Pick a license (MIT).
- [ ] Finalize the A team and B team for each preset roster.
- [ ] Make a good icon (the site still uses Astro's default favicon).
- [ ] Human pass on the "Give your poor human brain a rest" section copy. It's not doing the trick yet.
- [ ] Write the agent setup instructions: what an agent reads and does when told "Set up rostr for me by following the
      instructions at https://getrostr.sh".

## CLI build

- [ ] One CLI in Go, built for macOS, Linux, and Windows, used the same way by local and cloud sessions.

## Cloud auth

- [ ] One secret key per person, no account. `rostr transfer` gets a `rostr_sk_…` key from the server and saves it to
      `~/.config/rostr/credentials`. The key owns all of that person's rosters and project links.
- [ ] A machine is in cloud mode whenever it has a key (`credentials` or `ROSTR_KEY`); otherwise everything is local.
      No mixing local and cloud rosters.
- [ ] `rostr key` prints the key; `rostr key rotate` replaces it.
- [ ] Server stores only a hash of each key and rate-limits key creation by IP.
- [ ] No recovery in v1: a lost key means lost cloud rosters.

## Session-start hook

- [ ] Read `session_id` from the hook's JSON input and run `rostr draft --session <id>`.
- [ ] Write `export ROSTR_SESSION=<id>` to `CLAUDE_ENV_FILE` so rostr commands the agent runs know their session.
- [ ] Set the agent's first task from the first prompt (`rostr assign --session <id> "<summary>"`).
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

- [ ] Make the repo public so `rostr submit` and the site's "Submit your own" link work.
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

- [ ] Team rosters: share a roster with teammates (would need a team key on top of per-person keys).
