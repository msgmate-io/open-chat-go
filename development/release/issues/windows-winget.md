# [Release] winget package for open-chat (Windows)

## Context
Issue #234 recommends winget as the primary Windows channel and ships the
manifest templates + renderer in `development/windows/winget/`. A listing is not
possible until the release publishes per-arch `.zip` archives containing a stable
`open-chat.exe`; today only versioned bare `.exe` files exist, which would expose
an ugly command alias.

## Goal
Publish `Msgmate.OpenChat` to `microsoft/winget-pkgs` and automate version bumps
for production releases.

## Research questions
- Add a `build.yaml` step producing
  `open-chat-<version>-windows-{amd64,arm64}.zip` (containing `open-chat.exe`)
  next to the existing `.exe` assets, then verify `winget validate` accepts the
  rendered manifest.
- Submission identity: which GitHub account/token opens the version PR against
  `microsoft/winget-pkgs`, and are automated PRs acceptable for this publisher?
- Automation: reusable `windows-package-managers.yaml` (modelled on
  `homebrew-formula.yaml`) using `wingetcreate update ... --submit`, or
  `render_manifest.sh --submit`.
- Indirect-URL validation: confirm version-pinned
  `releases/download/<tag>/<asset>` URLs pass winget's installer URL checks.
- Service-binary upgrade: decide between registering the SCM service against the
  package-manager binary path and adding explicit `open-chat stop/start/upgrade`
  subcommands so `winget upgrade` can refresh the running service.

## Deliverables
- `.zip` packaging in `build.yaml`.
- First `microsoft/winget-pkgs` manifest PR for `Msgmate.OpenChat`.
- Release automation that renders and submits subsequent versions.
- Root `README.md` winget install instructions.

## Acceptance criteria
- `winget install Msgmate.OpenChat` installs `open-chat` on Windows x64 and
  arm64, and `open-chat --help` works.
- `winget validate --manifest` passes in CI.
- The winget package is listed in `microsoft/winget-pkgs`.
- `.github/workflows/windows-install-verify.yaml` passes on `windows-latest`
  against a release that includes the `.zip` assets.

## References
- `development/windows/README.md`
- `development/windows/winget/render_manifest.sh`
- `.github/workflows/build.yaml`, `development/homebrew/render_formula.sh`