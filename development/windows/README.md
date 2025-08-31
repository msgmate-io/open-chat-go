# Windows distribution for open-chat

Research and recommendation for how open-chat should be installed and updated on
Windows. This document is the deliverable of
[#234](https://github.com/msgmate-io/open-chat-go/issues/234); the machine
readable artefacts live next to it:

| Path | Purpose |
| --- | --- |
| `winget/` | winget manifest templates and the shared renderer. |
| `scoop/` | Scoop bucket recipe template and bucket maintenance guide. |
| `tests/` | Offline render test and release fixture. |
| `.github/workflows/windows-install-verify.yaml` | Weekly/manual Windows install + service verification. |
| `../release/issues/windows-*.md` | Follow-up implementation tickets. |

## TL;DR

- **Primary channel: winget** (`Msgmate.OpenChat`) — preinstalled on current
  Windows, handles x64/arm64, user-scope install.
- **Secondary channel: Scoop** (`msgmate-io/scoop-bucket`) — admin-free install
  for users who already use Scoop.
- **Portable fallback: per-arch `.zip`** (stable `open-chat.exe`), with the bare
  `.exe` release assets kept as a last resort.
- **`open-chat install` is enough for v1.** No MSI/MSIX is required to run the
  server as a native Windows service; package managers only place the binary.
- **MSI is deferred** to a follow-up; **MSIX is ruled out** (package identity
  cannot register a classic Windows service); **Chocolatey is deferred**
  (x64-centric, needs a preinstalled client and feed moderation).

The single biggest enabling gap is that releases currently publish *versioned
bare `.exe` files* (`open-chat-0.0.614-windows-amd64.exe`). Installers built
from those would expose an ugly command alias. We should ship per-arch `.zip`
archives containing a stable `open-chat.exe`, which then allows winget
`InstallerType: zip` + `NestedInstallerType: portable` +
`PortableCommandAlias: open-chat` and a clean Scoop `bin` shim.

## Current state

**Release artifacts and CI**

- `.github/workflows/build.yaml` builds `windows/amd64` and `windows/arm64` on
  `ubuntu-latest` with `CGO_ENABLED=0`, `INTEGRATION_PROFILE=core-only` and
  `OPEN_CHAT_RELEASE_BUILD=1`.
- Assets are named `open-chat-<version>-windows-<arch>.exe` (for example
  `open-chat-0.0.614-windows-amd64.exe`, `...-arm64.exe`). No `.zip` is built.
- The `release` job publishes every artifact; the `homebrew` job is the model for
  a post-release distribution automation to copy.

**Windows service support already exists in the CLI**

- `backend/cmd/service_path_windows.go` uses `%ProgramFiles%\OpenChat` as the
  install directory and `%ProgramData%\OpenChat` as the work directory, detects
  an elevated token, persists the install dir in the HKLM/HKCU `Environment`
  `Path` value and broadcasts `WM_SETTINGCHANGE`.
- `backend/cmd/install.go` implements `open-chat install`: it copies the running
  executable to the install directory, writes a 0600 `open-chat.json` env config,
  registers the service through `kardianos/service` (the Windows SCM backend) and
  optionally starts it. `--force`, `--install-dir`, `--workdir`,
  `--root-credentials`, `uninstall` and `status` all exist.
- `github.com/kardianos/service v1.3.0` supports `windows/arm64`, and Go builds
  `windows/arm64`. A native arm64 service path therefore already works.

**Known rough edges**

- `open-chat install` *copies* the binary into `%ProgramFiles%\OpenChat\open-chat.exe`
  and registers the service against that copy (`install.go:190-217`), then adds
  `%ProgramFiles%\OpenChat` to the machine `PATH` (`install.go:222`). Package
  manager updates therefore do not update the running service binary. See
  [Service-binary copy](#service-binary-copy-and-the-upgrade-runbook).
- `var VERSION` lives in `backend/api/metrics/handler.go` and is bumped by
  `full_build.sh` during the build, so the release **tag** and the asset
  **version** can differ (observed: tag `...0.0.613...`, assets `0.0.614`).
  `development/homebrew/render_formula.sh` parses the version from the asset name
  for this reason; the Windows renderer does the same.
- **No `LICENSE` file** is present (`gh api` reports `license: null`). winget's
  default-locale manifest requires `License`/`LicenseUrl` and Scoop exposes a
  `license` field. This is a hard prerequisite for a quality listing.
- **No Authenticode signing** anywhere in CI; binaries are unsigned.
- There is no `msgmate-io/scoop-bucket` repository and no winget package yet.
- `scoop` is not preinstalled on Windows (winget and PowerShell are).

## Recommendation

### winget as primary

winget (`winget install Msgmate.OpenChat`) is preinstalled on Windows 10/11 and
Windows Server images, supports per-architecture installers, validates
submissions in CI and gives users a familiar upgrade path. The package lands in
the user scope; service registration remains an explicit elevated step.

Package identity:

- `PackageIdentifier: Msgmate.OpenChat`
- Publisher: `Msgmate`, manifest path `manifests/m/Msgmate/OpenChat/<version>/`
- Moniker: `open-chat`

`OpenChat.OpenChat` is a viable alternative identifier (Publisher `OpenChat`);
whichever is chosen must stay consistent with the ARP metadata an eventual MSI
would write.

### Scoop as secondary

`msgmate-io/scoop-bucket` gives admin-free install/update for users who already
run Scoop, with `architecture.64bit`/`arm64` selection and `checkver`/`autoupdate`
support. Publishing is just a commit to the bucket repository, so it is not
blocked on upstream review.

### Portable `.zip` as fallback

Keep publishing the per-arch `.zip` (and the legacy bare `.exe`) so users without
a package manager can download, extract and run `open-chat install` from an
elevated prompt. The `.zip` also makes the winget/Scoop manifests produce a
clean `open-chat` command.

### Ruled out / deferred

- **MSIX — ruled out.** Packages run with package identity, cannot install or
  control a classic Windows service, and cannot freely write HKLM/PATH or
  self-elevate. It is a poor fit for a service that also needs `%ProgramFiles%`
  and machine `PATH`.
- **MSI (WiX v4/v5 + Burn) — deferred.** Only worth it once we need a one-step
  elevated install that also registers the service, an ARP entry for
  inventory, or GPO/SCCM deployment. Tracked in `windows-msi-optional.md`.
- **Chocolatey — deferred (Phase 5).** x64-centric, requires a preinstalled
  Chocolatey client, community feed moderation, and the push API needs a paid
  account for higher rate limits. It becomes the natural wrapper once an MSI
  exists because package scripts run elevated.

## Do we need a native installer?

**No, not for v1.** `winget install` / `scoop install` lay down the binary, and
the documented, elevated `open-chat install` registers and starts the service.
The service implementation (SCM registration, install dir, work dir, PATH) is
already shipped and arm64-capable.

An MSI becomes worth building only when at least one of these is required:

1. a single elevated command that installs *and* registers the service,
2. an Add/Remove Programs entry and uninstall story for managed fleets,
3. GPO/SCCM/Intune deployment, or
4. a Chocolatey package that wants to run the service install as part of its
   (elevated) install script.

## Tradeoff matrix

| Channel | Availability | Elevation UX | Service install | Auto-update | SmartScreen / AV | arm64 | Maintenance | Needs signing |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **winget** | Built into Win 10/11 | User-scope install; service needs one elevated `open-chat install` | Via CLI (explicit) | `winget upgrade` | Better once signed; winget scans binaries | Yes (x64/arm64) | Medium (PR review per version) | Strongly recommended |
| **Scoop** | Installed by user | None for install; service needs elevation | Via CLI (explicit) | `scoop update` | Better once signed | Yes (x64/arm64) | Low (commit to bucket) | Recommended |
| **Chocolatey** | Preinstalled client needed | Package script can elevate | Via CLI/script | `choco upgrade` | Better once signed | Weak | Medium (moderation/paid API) | Recommended |
| **MSI / Burn** | Manual download | Single elevated installer | Could do it in the installer | ARP/GPO, not self-updating | Better once signed | Yes | High (WiX + signing) | Yes (expected) |
| **MSIX** | Store/sideload | N/A — | **Cannot register a classic service** | Store | Package identity | Yes | High | Yes |
| **Portable `.zip`/`.exe`** | Direct download | Manual `open-chat install` | Via CLI (explicit) | Manual | Worse (no signed installer) | Yes | Lowest | Optional |

## arm64 status

The Go build matrix already emits `windows/arm64` and `kardianos/service`
delegates to the SCM on `windows/arm64`, so native service management works.
Ship both architectures and let winget/Scoop select the matching archive. Where
a native arm64 build is not used, Windows 11's x64 emulation can run the amd64
build as a fallback. GitHub-hosted arm64 Windows runners (`windows-11-arm`) are
in preview; the verification workflow defaults to x64 and can be extended to
arm64 once the runner label is enabled for the repository.

## Code signing

Unsigned downloads trigger SmartScreen warnings and increase AV/PUA false
positives; winget's own binary scan can reject installers that fail across AV
engines. Options:

| Option | Notes | Rough cost |
| --- | --- | --- |
| **OV Authenticode** | Since 2023 CA/B requires OV keys on FIPS 140-2 Level 2+ hardware; SmartScreen reputation builds over time. | ~$200–500/yr |
| **EV Authenticode** | Immediate SmartScreen reputation; hardware token/HSM required. | ~$300–700/yr |
| **Azure Trusted Signing** (now branded **Artifact Signing**) | Managed, CI-friendly (jsign / `trusted-signing` task); organizations generally need 3+ years of verifiable business history. | ~$10/mo Basic |

CI integration: sign the Windows binaries in `build.yaml` before upload using
`signtool` (Windows runner) or Azure Trusted Signing from the Linux runners, and
always apply an RFC 3161 timestamp so signatures outlive the certificate.
Signing is tracked as a separate follow-up (`windows-code-signing.md`) because
it requires an external identity and account setup that a code PR cannot
complete.

## Service-binary copy and the upgrade runbook

`open-chat install` copies the binary to `%ProgramFiles%\OpenChat\open-chat.exe`
and the service points at that copy. After a package-manager update the managed
copy in the package manager's links directory changes but the service copy does
not. Additionally `%ProgramFiles%\OpenChat` is added to the machine `PATH` while
the package manager adds its own links directory, so `open-chat` may resolve
from either location depending on `PATH` order.

**Required upgrade runbook (documented for users):**

```powershell
winget upgrade Msgmate.OpenChat     # or: scoop update open-chat
open-chat stop                      # if a stop subcommand is added; otherwise use sc.exe
open-chat install --force           # elevated; re-copies the binary + restarts
open-chat status
```

Two clean long-term fixes, tracked in `windows-winget.md`:

1. Register the service against the package-manager-managed binary path instead
   of copying it (requires SCM reconfiguration and dealing with locked files
   during upgrades), or
2. Add explicit `open-chat upgrade`/`open-chat stop`/`open-chat start`
   subcommands that stop the service, replace the binary and restart.

Until then the runbook above is the supported path.

## Prerequisites

1. **License decision + `LICENSE` file** — needed for winget `License`/
   `LicenseUrl` and Scoop `license`. The renderer defaults to `Proprietary`
   (accurate while no license is declared) and the templates must be updated
   once an SPDX id is chosen.
2. **Publisher identity / PackageIdentifier sign-off** — `Msgmate.OpenChat`.
3. **Per-arch `.zip` release assets** containing a stable `open-chat.exe`
   (`build.yaml` change).
4. **Authenticode signing** (recommended before public listing).
5. **`msgmate-io/scoop-bucket` repository** and a token with write access.
6. **winget submission identity** — a GitHub account/token allowed to open the
   version PR against `microsoft/winget-pkgs`.

## Phased plan

- **Phase 0 — prerequisites (this document):** recommendation, templates,
  renderer, offline test, follow-up tickets. *Done in #234.*
- **Phase 1 — zip packaging:** extend `build.yaml` to produce
  `open-chat-<version>-windows-{amd64,arm64}.zip` (containing `open-chat.exe`)
  alongside the existing `.exe`. Add a Windows arm64 verification run.
- **Phase 2 — Scoop:** create `msgmate-io/scoop-bucket`, publish the rendered
  `bucket/open-chat.json`, add the release automation.
- **Phase 3 — winget:** render the manifests for a real release and open the
  first PR against `microsoft/winget-pkgs`; then automate subsequent version PRs.
- **Phase 4 — signing:** sign the Windows binaries in CI (Azure Trusted
  Signing), re-submit or refresh the winget package.
- **Phase 5 — MSI + Chocolatey (optional):** WiX v4/v5 + Burn installer, then a
  Chocolatey package wrapping it.

## Rendered manifest / recipe examples

The templates in this directory are the source of truth and contain `@PLACEHOLDER@`
markers. `winget/render_manifest.sh` substitutes them from a release. For a
release with version `0.0.614` the winget installer manifest renders as:

```yaml
PackageIdentifier: Msgmate.OpenChat
PackageVersion: "0.0.614"
Platform:
  - Windows.Desktop
MinimumOSVersion: "10.0.0.0"
InstallModes:
  - silent
  - silentWithProgress
Installers:
  - Architecture: x64
    InstallerType: zip
    NestedInstallerType: portable
    NestedInstallerFiles:
      - RelativeFilePath: open-chat.exe
        PortableCommandAlias: open-chat
    InstallerUrl: https://github.com/msgmate-io/open-chat-go/releases/download/open-chat-0.0.613/open-chat-0.0.614-windows-amd64.zip
    InstallerSha256: <sha256>
    Scope: user
    UpgradeBehavior: install
  - Architecture: arm64
    InstallerType: zip
    NestedInstallerType: portable
    NestedInstallerFiles:
      - RelativeFilePath: open-chat.exe
        PortableCommandAlias: open-chat
    InstallerUrl: https://github.com/msgmate-io/open-chat-go/releases/download/open-chat-0.0.613/open-chat-0.0.614-windows-arm64.zip
    InstallerSha256: <sha256>
    Scope: user
    UpgradeBehavior: install
ManifestType: installer
ManifestVersion: 1.10.0
```

`PortableCommandAlias` is only valid for the archive + nested-portable case,
which is why the `.zip` asset is a prerequisite. The Scoop recipe
(`scoop/open-chat.json.tmpl`) sets `"bin": "open-chat.exe"`, `checkver` on the
`open-chat-<version>-windows-amd64` asset and `autoupdate` for both arches with
`hash.mode: download`.

Render and test locally (offline, no release required):

```bash
development/windows/tests/render_manifest_test.sh
```

Render against a real release once `.zip` assets exist:

```bash
GITHUB_TOKEN=... development/windows/winget/render_manifest.sh \
  --tag open-chat-0.0.614 --output-dir dist/windows-packages
```

## Release and upgrade runbooks

### Maintainer: publish a Windows release

1. Confirm the release contains `open-chat-<version>-windows-amd64.zip` and
   `...-arm64.zip` (Phase 1+).
2. Decide/verify the license identifier.
3. Render: `render_manifest.sh --tag <tag> --output-dir dist/windows-packages`.
4. winget: open/refresh the `microsoft/winget-pkgs` PR, or run
   `render_manifest.sh --tag <tag> --submit` on Windows with `wingetcreate`.
   Validate with `winget validate --manifest dist/windows-packages/winget`.
5. Scoop: copy `dist/windows-packages/scoop/open-chat.json` into
   `msgmate-io/scoop-bucket/bucket/` and commit.
6. Update the Windows section of the root `README.md` if the channel set changes.

### User: install

```powershell
winget install Msgmate.OpenChat      # or: scoop install msgmate/open-chat
open-chat install                    # elevated
open-chat status                     # service: running
```

### User: upgrade

```powershell
winget upgrade Msgmate.OpenChat      # or: scoop update open-chat
open-chat install --force            # elevated: refresh the service binary
open-chat status
```

## CI automation

- **Build:** Phase 1 adds the `.zip` step to `build.yaml`. Phase 4 adds signing.
- **Publish:** a reusable `windows-package-managers.yaml` (modelled on
  `homebrew-formula.yaml`) should be called by `build.yaml` for production
  releases to render and submit/commit the manifests.
- **Verify:** `.github/workflows/windows-install-verify.yaml` renders the
  manifests from a fixture on every PR touching these assets, and on
  `workflow_dispatch`/weekly runs a real end-to-end check on `windows-latest`:
  download the latest release asset, `open-chat --help`, `open-chat install
  --force`, poll `http://127.0.0.1:1984/api/version`, assert `open-chat status`
  reports the service running, then `open-chat uninstall --purge`.

## Verification

- `development/windows/tests/render_manifest_test.sh` renders from a fixture and
  asserts the version is derived from the asset name (`0.0.614`, not the tag's
  `0.0.613`), that no `@...@` placeholders remain, that all four files are
  written and that the Scoop recipe is valid JSON.
- `windows-install-verify.yaml` runs `shellcheck` and the render test on pull
  requests, and the full service install/uninstall flow on manual/weekly runs.

## Risks and open questions

- **License undeclared** — blocks a clean winget/Scoop listing; decide the SPDX
  id and add `LICENSE`.
- **Service-binary copy** — package manager upgrades do not update the running
  service; requires the documented `open-chat install --force` step and a
  follow-up fix.
- **Unsigned binaries** — SmartScreen reputation and winget's multi-AV scan are
  the main listing risks; signing should precede public submission.
- **winget-pkgs automation** — automated PRs must respect publisher identity,
  installer hosting on the publisher's release location, one version per PR and
  manual review latency; confirm the submitting account.
- **GitHub asset URL redirects** — use version-pinned
  `releases/download/<tag>/<asset>` URLs and verify with `winget validate`.
- **Version/tag skew** — manifest renderers must derive the version from the
  asset name, exactly like `development/homebrew/render_formula.sh`.
- **arm64 runners** — `windows-11-arm` availability is still to be confirmed for
  this repository; the verification workflow defaults to x64.

## Follow-up tickets

- [`windows-winget.md`](../release/issues/windows-winget.md) — zip packaging,
  renderer automation and the first `microsoft/winget-pkgs` submission.
- [`windows-scoop.md`](../release/issues/windows-scoop.md) — bucket repository,
  recipe publishing and release automation.
- [`windows-code-signing.md`](../release/issues/windows-code-signing.md) —
  Authenticode / Azure Trusted Signing in CI.
- [`windows-msi-optional.md`](../release/issues/windows-msi-optional.md) —
  optional WiX/Burn MSI and Chocolatey wrapper.