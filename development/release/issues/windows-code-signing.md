# [Release] Authenticode code signing for Windows binaries

## Context
Issue #234 found that Windows binaries are unsigned, which causes SmartScreen
warnings and AV/PUA false positives and risks winget's multi-engine binary scan.
Signing is a prerequisite for a smooth public listing on winget and Scoop.

## Goal
Sign the windows amd64/arm64 binaries (and any future MSI) in CI with a
timestamped Authenticode signature, and document certificate management.

## Research questions
- Certificate type: OV vs EV Authenticode vs Azure Trusted Signing
  (Artifact Signing). OV requires FIPS 140-2 Level 2+ hardware since 2023;
  Trusted Signing has organization-age eligibility requirements.
- CI integration: `signtool` on a Windows runner vs `jsign`/Trusted Signing from
  the existing Linux build matrix; where the signing step belongs in
  `build.yaml` (before the artifact upload).
- Timestamping: RFC 3161 TSA endpoint and retry behaviour.
- SmartScreen reputation: what the certificate type implies for first-run
  warnings and how long reputation takes to build.
- Secret management: where the certificate/key and TSA credentials live, and how
  forks build unsigned without failing.
- Whether signing changes the `.zip` and winget installer sha256 values
  (it does — manifests must be rendered after signing).

## Deliverables
- Signing step in `build.yaml` for `windows/amd64` and `windows/arm64`.
- Timestamped signatures verified in CI (`signtool verify /pa` or `jsign`).
- Documentation for certificate/key rotation and cost.

## Acceptance criteria
- Release Windows binaries carry a valid, timestamped Authenticode signature.
- `signtool verify /pa` succeeds on both architectures.
- Manifests are rendered from the signed assets so hashes match.
- Forks and repositories without signing secrets still build (unsigned).

## References
- `.github/workflows/build.yaml`
- `development/windows/README.md` (code-signing section)
- Azure Trusted Signing / Artifact Signing documentation