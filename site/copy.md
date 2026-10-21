# rostr site copy

All human-facing copy on getrostr.sh, one section per page, in page order.

Left out: code blocks and terminal output, the mockups (notifications, dashboard), the hero's rotating status line, roster names and members (`src/data/rosters.ts`), the legal pages (`src/legal/*.md`), and the agent-facing setup docs (`public/setup.md`, `llms.txt`, `agents.md`).

---

# Page: Home (`/`)

## Give your agents names.

Remember who's doing what.

rostr automatically drafts a name for each agent from a stable roster you set. It works across all your favorite harnesses in local and cloud sessions.

Get started: paste this into your agent

[button] “Set up rostr for me by following the instructions at https://getrostr.sh” [Copy]

[or run the install script ↓](#install)

### How it works

rostr is a small [CLI](/cli) plus a skill that teaches your agent how to use it.

#### Assemble your roster

Run `rostr assemble`, or just ask your agent to do it for you.

#### Sessions draft agents

When a session starts, a hook drafts the next available name and tells the agent its identity.

#### Done? Back to the bench

When a session ends, its agent is free for the next one.

The commands, at a glance:

#### 🎯 Roster commands

Assemble rosters, give projects their own, see who's on what, and edit your cast.

#### 🤖 Agent commands

Your hooks run these for you. Draft by hand with /draft.

#### ⚙️ Settings

Emoji, auto-draft, cloud, and your default roster.

[Read the full CLI docs →](/cli)

### Why anthropomorphize your agents?

A bunch of reasons, it turns out.

#### Triage notifications

Know who's asking before you click, just by referencing an emoji.

#### Exploit your human psychology

Your brain doesn't want to remember 25 abstract "work streams". It may remember that Aragorn and Sauron are tag-teaming the CI fix.

#### Better async

When an agent reaches out on Slack, a PR, or email, it signs with its name, so you know exactly which session to open.

#### Help agents find each other

Names give agents a way to address each other. Point one at another by name and it knows who you mean.

#### It's funny

Gandalf fixing your CSS is objectively funnier than session 4f2a fixing your CSS.

### Squad up

Pick a pre-built roster or assemble your own. Every roster has an A team and a B team (hover to meet them), and every agent gets its own emoji, which you can turn off if that's not your thing.

- 💍**The Fellowship** — The Lord of the Rings

- ⚡**Hogwarts** — Harry Potter

- 🎮**Smash Bros** — Super Smash Bros.

- 🏀**NBA Legends** — Basketball

- 🌌**The Rebellion** — Star Wars

- 📎**Dunder Mifflin** — The Office

- 💰**Billionaires** — Put them to work

- 🧑‍💻**CS Heroes** — Computer science

- 🛡️**Avengers** — Marvel

- 🖖**The Enterprise** — Star Trek: TNG

- Random🎲**Free Agents** — Feeling chaotic?

- ✏️**Your own** — Any list of names

[See all pre-built rosters + submit your own →](/rosters)

#### 🅰️ A team, B team

Twenty names are too many to get to know, so every roster has a small A team: six by default, picked with `rostr edit`. rostr always drafts from the A team first and only calls up the B team when all your starters are busy.

#### 📁 One roster for work, one for side projects

Keep as many rosters as you like and give each project its own with `rostr link`. The Fellowship works your day job and the NBA Legends work your side projects, so when Jordan pings you, you know which one it is before you read a word.

### Local and Cloud Rosters

💻 Local

The default. Nothing leaves your machine.

☁️ Cloud

For when you also run agents in the cloud. Every session shares one roster, with no account, just a key. Task and repo metadata are end-to-end encrypted. [Learn more about privacy →](/privacy)

Choose during `rostr assemble`, or move to the cloud later with `rostr config cloud on`.

### Build on it

Use the output of `rostr list --json` or the list API endpoint to build your own dashboards, monitoring, and integrations.

A team dashboard built on `rostr list --json`.

#### 💻 On your machine

Local or cloud mode. Tasks and repos come back decrypted.

#### 🌐 On the web

Cloud mode. Get your token with `rostr config key --token`. Rosters and who's drafted come back readable; tasks and repos stay end-to-end encrypted.

[button] [📋 Copy a prompt that teaches your agent to decrypt them]

#### Response

### Why I built this

A few threads led to rostr:

- Once you adopt a long-running, async agent workflow, your agents need a way to **reach you when they get stuck**. And whatever tool you build or adopt for this, you should be able to tell at a glance which session is reaching out.

- Stable agent identities seem like a useful primitive! Regardless of what harness or agent management tool you use, you should be able to easily port your team. I was inspired by the named agents in [Gas Town](https://github.com/gastownhall/gastown), but without the whole kitchen sink.

- rostr is an experiment in exercising some restraint with agents. It's tempting to spin up countless sessions on experimental workstreams, get exhausted, and then lose context picking your work back up the next morning. A fixed team might nudge you to bite off a more human-manageable amount of work and prioritize around your team's size, the way an EM does.

### Install

#### Ask your agent

Point Claude Code or Codex at this page and it will install rostr, set up the hooks and skill, and help you pick a roster. Agents: your setup steps are at [getrostr.sh/setup.md](/setup.md).

[button] “Set up rostr for me by following the instructions at https://getrostr.sh” [Copy]

#### Run the install script

Install rostr, then assemble your roster. It walks you through the choices and sets up the hooks. [See the script](https://github.com/piazzatron/rostr/blob/main/site/public/install).

On a Mac with Homebrew, `brew install piazzatron/tap/rostr` works too.

**Cloud sessions** (Claude Code on the web, Codex cloud): add the install line to your environment's setup script, set `ROSTR_KEY` to the key from `rostr config key`, and allow `github.com` and `api.getrostr.sh` in its network settings.

---

# Page: CLI docs (`/cli`)

## CLI docs

rostr is a single program for macOS and Linux. Your hooks call it at the start and end of every session, and the rostr skill teaches your agent to use it too.

### Concepts

Roster

A named list of agents you can draft from, like your Fellowship or your Smash Bros.

A team and B team

Every roster has a small A team, six by default. Drafting picks from the A team first and only calls up the B team when every starter is busy.

Local or cloud

Your whole setup lives in one place. Locally, everything is on disk. Once you turn on [cloud mode](#config), everything lives on the rostr API at `api.getrostr.sh`, and every session, on your machine or in a cloud environment, drafts from there. Having a rostr key (saved by `rostr config cloud on`, or set as `ROSTR_KEY`) is what puts a machine in cloud mode.

Projects

Each git repo can use its own roster. rostr identifies a project by its `origin` remote (`owner/repo`), so it's the same project on every machine and in every worktree.

Sessions

Your hooks get each session's ID from Claude Code or Codex and pass it to rostr, so drafting, setting the first task, and releasing all happen without your agent doing anything. The start hook also sets `ROSTR_SESSION` in the session's shell, so any rostr command your agent runs knows which session it's in.

Which roster gets drafted

The `--roster` flag on agent commands if you pass one, then the project's linked roster, then your default. Outside a git repo, rostr uses your default.

### Roster commands

Set up and manage your rosters.

#### `rostr assemble`

Interactive setup: answer a few questions and you're done. It's the only command you need to run, and your agent can run it for you with flags.

`--preset <id>`

Skip the preset question. See them all with rostr presets.

`--names <list>`

Use your own comma-separated names, with an emoji in front if you like. The first six are your A team, and names without an emoji get a random one.

`--emoji on|off|only`

How agents show up. Defaults to on.

`--cloud`

Keep everything on the rostr API. Only for your first roster; after that, use rostr config cloud on.

`--no-auto-draft`

Don't draft sessions automatically; use /draft instead.

`--link`

Use the new roster for the repo you're in. The wizard asks this when you're in a repo.

#### `rostr link / unlink`

Pick which roster your project uses, or unlink it to go back to your default roster. Run rostr link on its own to see which roster this project uses and change it.

💡 Worth it when you want to tell projects apart at a glance: give your side projects the NBA Legends, and Jordan can only mean a side project.

`--repo <owner/repo>`

Link or unlink a repo other than the current one.

#### `rostr list`

See who's working on what, and who's idle, across all your rosters. Name rosters to see just those.

`--json`

Print the result as JSON.

#### `rostr edit`

Open a roster in your editor to change its members, their emoji, and who's on the A team. To swap in a whole new cast, run rostr assemble and replace it.

#### `rostr presets`

See all the pre-built rosters you can pass to --preset. They're also on the rosters page, with their full teams.

#### `rostr disband`

Delete a roster, after asking. Projects that used it fall back to your default, and if it was your default, your oldest other roster takes over.

`--yes`

Skip the question. Agents need this, since they can't answer it.

### Agent commands

Your hooks, the rostr skill, and the /draft slash command run these.

#### `rostr agent draft / release`

Draft the next free agent when a session starts, A team first, and send it back to the bench when the session ends. The same session always gets the same name back.

`--task <description>`

What the agent is working on. Shown in rostr list.

`--session <id>`

The session to draft or release for. The hooks pass this, and it defaults to ROSTR_SESSION.

`--json`

Print the drafted agent as JSON.

#### `rostr agent task`

Set or change what an agent is working on. Your hooks set the first task from your first prompt.

`--session <id>`

Set the task for whoever is drafted to this session. The hooks pass this, and it defaults to ROSTR_SESSION.

#### Slash command

If you'd rather draft by hand, turn auto-draft off and use this in Claude Code. `rostr assemble` installs it.

`/draft`

Draft an agent for this session. Runs `rostr agent draft`.

### Settings

Change anything you chose during assemble.

#### `rostr config`

See and change your settings. Run it on its own to see them all.

`default`

The roster projects use when they don't have a link.

`emoji`

How agents show up: on (🧙 Gandalf), off (Gandalf), or only (🧙). only needs every member to have a different emoji.

`auto-draft`

Draft new sessions automatically, or by hand with /draft.

`cloud on`

Move your whole setup to the cloud. Turn it on once, when you start using cloud agents. There's no going back to local.

`key`

Print your rostr key, which puts a machine in cloud mode. There's no recovery, so keep it safe. --token prints the token for the API instead.

---

# Page: Pre-built rosters (`/rosters`)

## Pre-built rosters

Pick one with `rostr assemble --preset <id>`, or let `rostr assemble` walk you through it. Made one you love? [Submit your own →](https://github.com/piazzatron/rostr/issues/new?labels=roster-submission&title=Roster+submission:+)

---

# Page: Privacy (`/privacy`)

## Privacy

Local mode makes no network calls. Cloud mode stores rosters and draft state on the rostr API; tasks and repo names are encrypted on your machine before they're sent.

### Data

Data

Local

Cloud (server)

Rosters

Disk

Plaintext

Drafts (member, session ID, timestamps)

Disk

Plaintext. Session IDs are random

Tasks

Disk

AES-256-GCM ciphertext

Repo identity (owner/repo)

Disk

HMAC-SHA256 fingerprint + AES-256-GCM ciphertext

rostr key

—

Never transmitted

### Keys

The CLI derives three independent keys from your rostr key. Only the auth token leaves your machine.

- **Auth token:** bearer credential for the API. The server stores only its SHA-256 hash.

- **Encryption key:** encrypts tasks and repo names with AES-256-GCM, with a random 96-bit nonce per value.

- **Fingerprint key:** HMAC-SHA256 over `owner/repo`, so the server can match project links without learning repo names.

Distinct HKDF labels make the keys independent: none reveals the rostr key or any other derived key.

### What the server sees

- **Visible:** roster contents, which members are drafted and when, session IDs, repo fingerprints, and the size and timing of task ciphertext.

- **Not visible:** your rostr key, task plaintext, repo names, prompts, or code.

### Consequences

- **No recovery.** A lost key means your cloud data is unrecoverable, by you or by us.

- **API responses** contain task ciphertext. `rostr list --json` decrypts it for you.

- **Cloud environments** need `ROSTR_KEY` to decrypt tasks. Never commit it.

---

# Shared: nav and footer

- [rostr](/)
- [How it works](/#how-it-works)
- [CLI docs](/cli)
- [Install](/#install)
- [GitHub](https://github.com/piazzatron/rostr)

- Made with ❤️ in Tokyo by [Michael Piazza](https://x.com/piazzatron) · © 2026 Rosebud Labs
- [Privacy policy](/privacy-policy) · [Terms](/terms) · [GitHub](https://github.com/piazzatron/rostr) · [X](https://x.com/piazzatron)
