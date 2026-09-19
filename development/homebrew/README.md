# open-chat Homebrew tap (macOS)

Install and manage open-chat on macOS with Homebrew.

```sh
brew install msgmate-io/tap/open-chat
brew services start open-chat
open-chat status
```

The server listens on `127.0.0.1:1984` and stores its SQLite database under
`$(brew --prefix)/var/open-chat`. `brew services` writes logs to
`$(brew --prefix)/var/log/open-chat.log` and `open-chat.err.log`. The root
credentials default to `admin:random`; the generated password is printed to the
log file the first time the server bootstraps its database.

Upgrade and lifecycle:

```sh
brew update
brew upgrade open-chat
brew services restart open-chat
brew services stop open-chat
brew uninstall open-chat
```

## Layout

| Path | Purpose |
| --- | --- |
| `development/homebrew/Formula/open-chat.rb.tmpl` | Formula template with `@VERSION@`, `@DARWIN_*_URL@` and `@DARWIN_*_SHA256@` placeholders. |
| `development/homebrew/render_formula.sh` | Resolves a release's darwin assets and sha256 digests, renders the formula and optionally publishes it to the tap. |
| `.github/workflows/homebrew-formula.yaml` | Reusable workflow that renders and publishes the formula. Called by `build.yaml` for production releases. |
| `.github/workflows/homebrew-macos-verify.yaml` | macOS end-to-end check: installs from the tap, starts/registers the `brew services` launchd agent and asserts the server answers. Runs on every push to `main`, weekly, and on demand. |
| `msgmate-io/homebrew-tap` | The public tap repository containing the rendered `Formula/open-chat.rb`. |

## Strategy

- **Tapping:** `msgmate-io/homebrew-tap` maps to the `msgmate-io/tap` short name, so
  `brew install msgmate-io/tap/open-chat` works. A binary formula is used instead of a
  cask because the artifact is a plain CLI binary and `brew services` can manage it.
  homebrew-core is intentionally not targeted: it requires popularity/notability and a
  licence declaration that this project cannot satisfy yet.
- **Prebuilt vs source:** prebuilt darwin `arm64`/`amd64` binaries from the GitHub
  release are used; Go is not required on the user's machine.
- **Service management:** the formula uses the Homebrew `service do` DSL (launchd via
  `brew services`). The CLI's own `open-chat install` (kardianos/service) remains
  available for users who prefer a system-wide `launchd` daemon, but it is intentionally
  not invoked by the formula to avoid two competing service managers.
- **Gatekeeper:** release binaries are currently unsigned and unnotarized. Homebrew
  removes the quarantine attribute when installing a formula (`system "xattr"` is not
  needed), so the binary runs after `brew install`. If a user copies a binary manually
  they may need `xattr -d com.apple.quarantine <binary>`.

## Releasing a new formula

Production releases bump the formula automatically: `build.yaml` creates the
`open-chat-<version>` release and then calls `homebrew-formula.yaml`, which renders the
formula from that release's darwin assets and commits it to `msgmate-io/homebrew-tap`.

To do it by hand (for example to bootstrap or repair the tap):

```sh
GITHUB_TOKEN=... development/homebrew/render_formula.sh \
  --tag open-chat-0.0.620 \
  --tap-repo msgmate-io/homebrew-tap
```

Use `--dry-run` to print the rendered formula without publishing, and `--output <file>`
to persist it. Publishing requires a token with `contents: write` on the tap
repository. The reusable workflow passes `secrets.BOT_TOKEN`; if that token lacks
cross-repo access the step logs a warning instead of failing the release.

The version is parsed from the release asset name (`open-chat-<version>-darwin-arm64`)
rather than the tag, because the build bumps `VERSION` after the tag is created.

## Continuous verification

`homebrew-macos-verify.yaml` runs the acceptance flow on real GitHub-hosted macOS
runners (`macos-latest` arm64 and `macos-15-intel` Intel). It installs
`msgmate-io/tap/open-chat`, runs the formula test, starts the `brew services`
launchd agent, waits for `127.0.0.1:1984` to answer and finally asserts that
`open-chat status` reports the server running. It is triggered by:

- every push to `main` (plus a weekly schedule to catch tap drift),
- PRs that touch this workflow or `development/homebrew/**`,
- manual `workflow_dispatch`/`workflow_call` (with `tap_repository`, `formula`
  and `service_port` inputs).

When a run fails, the "Wait for the server to answer" step prints the launchd
plist and the open-chat logs to make the failure actionable.

## Verifying a release manually

On a macOS 13+ machine:

```sh
brew install msgmate-io/tap/open-chat
brew services start open-chat
open-chat status   # service running + server answering on 127.0.0.1:1984
brew audit --formula msgmate-io/tap/open-chat
```

`brew audit` and `brew style` should be run before merging changes to the formula.