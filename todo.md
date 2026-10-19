# Todo

## Project

- [x] Pick a license (MIT).
- [ ] Finalize the A team and B team for each preset roster.
- [ ] Make a good icon (the site still uses Astro's default favicon).
- [ ] Write the agent setup instructions: what an agent reads and does when told "Set up rostr for me by following the
      instructions at https://getrostr.sh".

## CLI build

- [ ] One CLI in Go, built for macOS, Linux, and Windows, used the same way by local and cloud sessions.

## Cloud auth

- [ ] One secret key per person, no account. The first `rostr create --cloud` gets a `rostr_sk_…` key from the server
      and saves it to `~/.config/rostr/credentials`. The key owns all of that person's cloud rosters and project links.
- [ ] `rostr key` prints the key; `rostr key rotate` replaces it.
- [ ] Server stores only a hash of each key and rate-limits key creation by IP.
- [ ] No recovery in v1: a lost key means lost cloud rosters.

## Session-start hook

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

## Future roadmap

- [ ] Team rosters: share a roster with teammates (would need a team key on top of per-person keys).
