# Releasing rostr

Pushing a `v*` tag runs [`.github/workflows/release.yml`](.github/workflows/release.yml): it runs the CLI's tests,
then [GoReleaser](.goreleaser.yaml) builds rostr for macOS and Linux (amd64 and arm64), publishes a GitHub release
with the archives and `checksums.txt`, and pushes the Homebrew formula to
[`piazzatron/homebrew-tap`](https://github.com/piazzatron/homebrew-tap).

Those releases are what `brew install piazzatron/tap/rostr` and `curl -fsSL https://getrostr.sh/install | sh`
(`site/public/install`) install.

## Cut a release

From an up-to-date `main`:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Then watch the Release workflow in the repo's Actions tab.

## One-time setup

The workflow's own `GITHUB_TOKEN` can't push to the tap repo, so it needs a token that can:

1. Create a [fine-grained personal access token](https://github.com/settings/personal-access-tokens/new) with
   access to only `piazzatron/homebrew-tap` and the **Contents: Read and write** repository permission.
2. Add it as an Actions secret named `HOMEBREW_TAP_GITHUB_TOKEN` on `piazzatron/rostr` (Settings → Secrets and
   variables → Actions), or run `gh secret set HOMEBREW_TAP_GITHUB_TOKEN --repo piazzatron/rostr` and paste it.

The token expires, so renew it (and update the secret) before then.

