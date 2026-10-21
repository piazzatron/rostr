<p align="center">
  <a href="https://getrostr.sh">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset=".github/assets/banner-dark.png" />
      <img alt="rostr: Give your agents names. Remember who's doing what." src=".github/assets/banner-light.png" />
    </picture>
  </a>
</p>

<p align="center">
  <a href="https://getrostr.sh">Website</a> ·
  <a href="https://getrostr.sh/cli">CLI docs</a> ·
  <a href="https://getrostr.sh/rosters">Rosters</a> ·
  <a href="https://getrostr.sh/how-it-works">How it works</a>
  <br /><br />
  <a href="https://github.com/piazzatron/rostr/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/piazzatron/rostr/actions/workflows/ci.yml/badge.svg?branch=main" /></a>
</p>

rostr drafts a name for each of your coding agents from a roster you set, and puts it at the front of the session
title: `🧙 Gandalf · Fix auth redirect loop`. It works across Claude Code, Codex, and terminal multiplexers, locally
and in the cloud.

> **Status:** early. Claude Code works locally and in the cloud; Codex support is on the way.

## Install

Paste this into your agent:

```text
Set up rostr for me by following the instructions at https://getrostr.sh
```

Or do it yourself:

```sh
brew install piazzatron/tap/rostr   # or: curl -fsSL https://getrostr.sh/install | sh
rostr assemble
```

## Development

- `cli/` — the `rostr` CLI and its agent hooks (Go). [`docs/cli-spec.md`](docs/cli-spec.md) is the source of truth
  for how it behaves.
- `site/` — getrostr.sh (Astro), deployed to Vercel
- `server/` — api.getrostr.sh, deployed to Railway
- `tests/` — end-to-end tests against a real server; run them with `tests/run.sh`

Releases are covered in [`RELEASING.md`](RELEASING.md).

## License

[MIT](LICENSE)
