# [Release] Optional MSI (WiX/Burn) and Chocolatey for open-chat

## Context
Issue #234 concluded that `open-chat install` is sufficient for v1 and that
winget + Scoop cover the package-manager use cases. An MSI becomes worthwhile
only for a single elevated install that also registers the service, an
Add/Remove Programs entry, GPO/SCCM/Intune deployment, or as the foundation of a
Chocolatey package.

## Goal
Decide whether to build a WiX v4/v5 MSI (optionally a Burn bundle) that installs
the binary and registers the Windows service in one elevated step, and, if built,
publish a Chocolatey package wrapping it.

## Research questions
- WiX v4/v5 authoring: per-machine install into `%ProgramFiles%\OpenChat`, work
  dir under `%ProgramData%\OpenChat`, service registration (native WiX
  `ServiceInstall` vs invoking `open-chat install`).
- Upgrade handling for the running service (major upgrade scheduling, restart
  manager, locked-file behaviour).
- ARP metadata and how it maps to the winget/Scoop `PackageIdentifier`
  (`Msgmate.OpenChat`).
- Signing requirements for the MSI and whether it changes the winget channel
  (an MSI installer type could replace the portable zip once signing exists).
- Chocolatey: community feed moderation, ARM64 support, the paid push API, and
  whether the package should bundle the MSI or install the portable binary.
- Whether the service-binary copy problem is better solved by the MSI than by a
  CLI `upgrade` subcommand.

## Deliverables
- Decision record: build the MSI/Chocolatey package, or keep winget + Scoop +
  `open-chat install` as the supported story.
- If built: WiX project, CI job producing a signed `.msi`, and a Chocolatey
  package definition.
- Install/uninstall/upgrade documentation.

## Acceptance criteria
- A single elevated command installs open-chat, registers the service and starts
  it on `127.0.0.1:1984`.
- Upgrade replaces the binary the service runs and restarts the service.
- Uninstall stops and removes the service, binary and (per policy) data.
- The MSI is Authenticode-signed and passes `msiexec /a` administrative-install
  validation.

## References
- `development/windows/README.md`
- `backend/cmd/install.go`, `backend/cmd/service_path_windows.go`
- WiX Toolset v4/v5 documentation, Chocolatey package guidelines