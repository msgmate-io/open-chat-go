# [Release] .AppImage release for open-chat

## Context
Provide a portable Linux `.AppImage` attached to GitHub Releases (x86_64; aarch64 optional).

## Goal
Research, plan and prepare a reproducible AppImage build, then implement.

## Research questions
- Tooling: appimagetool + AppDir vs linuxdeploy vs a GitHub Action vs GoReleaser + custom hook.
- Desktop integration: .desktop, icon, AppRun wrapper launching `open-chat server` with
  defaults (sqlite under $XDG_DATA_HOME, REDIS_MODE=embedded), optional browser open.
- glibc portability: current binary is dynamic; prefer CGO_ENABLED=0 static to avoid
  bundling glibc.
- First-run UX: root credentials generation, config/data dirs, single-instance behaviour.
- FUSE requirement and `--appimage-extract-and-run` fallback.
- Signing/updates: AppImage update info (zsync), optional AppImageHub listing.

## Deliverables
- Reproducible AppImage build script + CI job attaching the asset to Releases.
- .desktop + icon + AppRun. Docs entry.

## Acceptance criteria
- AppImage runs on Ubuntu 22.04/24.04 and Fedora without install.
- Server reachable at http://127.0.0.1:1984; data persisted under XDG dirs.
