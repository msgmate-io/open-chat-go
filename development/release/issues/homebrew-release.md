# [Release] Homebrew formula/tap for open-chat (macOS)

## Context
darwin amd64/arm64 binaries are already built by the matrix. We want `brew install` on macOS.

## Goal
Research, plan and prepare a Homebrew tap (formula and/or cask), then implement.

## Research questions
- Tap strategy: msgmate-io/homebrew-tap Formula/open-chat.rb (binary formula) vs
  `brew install --cask` vs homebrew-core (notability/licensing requirements).
- Prebuilt vs source build: binary formula with on_arm/on_intel url+sha256 per release;
  macOS Gatekeeper/quarantine and code signing/notarization (darwin binaries currently
  unsigned).
- `service do ... end` for `brew services` (launchd); `open-chat install` already supports launchd.
- CI automation to bump the formula on production releases (`brew bump-formula-pr`/audit).
- Intel (darwin-amd64) support viability.

## Deliverables
- Homebrew tap + formula, CI bump automation, install docs.

## Acceptance criteria
- `brew install msgmate-io/tap/open-chat` and `brew services start open-chat` work on
  macOS 13+ (arm64 and intel). `open-chat status` reports running.
