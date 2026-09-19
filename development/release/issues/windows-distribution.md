# [Release] Research & recommend Windows distribution for open-chat

## Context
windows amd64/arm64 .exe assets exist and the CLI implements a Windows service
(%ProgramFiles%\OpenChat, %ProgramData%\OpenChat, registry PATH, WM_SETTINGCHANGE).
The user asked us to research and recommend a Windows approach.

## Goal
Research and recommend a Windows distribution strategy (recommendation + follow-up tickets;
implementation optional in this issue).

## Research questions
- Compare winget (microsoft/winget-pkgs), Scoop bucket, Chocolatey, MSI (WiX/Burn), MSIX,
  and plain zip/portable. Consider service-install UX, elevation, auto-update, SmartScreen.
- Is an installer needed, or is `winget install` + `open-chat install` sufficient?
- arm64 Windows support status.
- Code signing: Authenticode cert (OV/EV), cost, signtool in CI, SmartScreen reputation.
- Auto-update: winget upgrade vs Scoop vs self-update.

## Deliverables
- Recommendation document with tradeoffs + phased plan and follow-up implementation tickets
  (e.g. winget manifest, Scoop bucket, optional MSI).

## Acceptance criteria
- Clear primary + secondary channel recommendation with rationale.
- Concrete manifest/recipe sketches and CI automation plan.
- Explicit prerequisites (code signing, publisher identity, versioning).
