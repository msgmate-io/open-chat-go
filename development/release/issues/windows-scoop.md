# [Release] Scoop bucket for open-chat (Windows)

## Context
Issue #234 recommends Scoop as the secondary Windows channel for users who want
an admin-free, user-scope install. The recipe template and bucket maintenance
guide already live in `development/windows/scoop/`; the bucket repository does
not exist yet.

## Goal
Create `msgmate-io/scoop-bucket`, publish `bucket/open-chat.json` and automate
recipe bumps on production releases.

## Research questions
- Bucket layout and naming: single `open-chat.json` in `bucket/`, plus a
  README and license for the bucket repository.
- Recipe publishing automation: commit the rendered recipe from CI using a bot
  token with `contents: write` on the bucket, or run `scoop checkver -u`
  (`scoop` is not preinstalled on GitHub-hosted Windows runners).
- `architecture.64bit`/`arm64` selection once the per-arch `.zip` assets exist.
- `checkver` regex robustness against the tag/asset version skew
  (`open-chat-([\d.]+)-windows-amd64`).
- Document the post-update `open-chat install --force` step caused by the
  service-binary copy.

## Deliverables
- `msgmate-io/scoop-bucket` repository with `bucket/open-chat.json`.
- CI automation that renders and commits the recipe for production releases.
- Install docs (`scoop bucket add msgmate ...` / `scoop install`).

## Acceptance criteria
- `scoop bucket add msgmate https://github.com/msgmate-io/scoop-bucket` and
  `scoop install msgmate/open-chat` install `open-chat` on Windows x64/arm64.
- `scoop checkver msgmate/open-chat -u` updates the recipe to the latest release.
- Hashes in the recipe match the published `.zip` assets.

## References
- `development/windows/scoop/README.md`
- `development/windows/winget/render_manifest.sh`