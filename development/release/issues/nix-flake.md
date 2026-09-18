# [Release] Nix package & flake for open-chat

## Context
open-chat ships as a single self-contained Go binary per platform (frontend, routes.json
and swagger are embedded via //go:embed in backend/server/routing.go). GitHub Releases
already publish linux/darwin/windows amd64+arm64 assets. We want a first-class Nix package
+ flake so NixOS users can `nix run` / install declaratively and self-host.

## Goal
Research, plan and prepare `flake.nix` (+ NixOS module), then implement on a separate PR.

## Research questions
- Build from source vs package prebuilt release binary:
  - `buildGoModule` must include git submodules because backend/go.mod has 15 local
    `replace` directives (frontend, clients/*). Flake inputs need `submodules = true`.
  - full_build.sh additionally runs the Vike/npm frontend build, `swag init` and
    resolve_integrations.py (integration profiles). Which can run in the Nix sandbox?
    (fetchNpmDeps/buildNpmPackage; package-lock.json exists.)
  - Alternative: fetchurl the release asset + autoPatchelfHook. Note current binaries are
    glibc-dynamic and unstripped — evaluate a CGO_ENABLED=0 static, stripped build first.
- Version: VERSION lives in backend/api/metrics/handler.go; inject from flake rev/tag via
  `-ldflags -X backend/api/metrics.VERSION=...`.
- NixOS module: hardened systemd service (binary already supports `open-chat install`),
  StateDirectory, config/env, REDIS_MODE=embedded vs external, sqlite path.

## Deliverables
- flake.nix exposing packages.<system>.open-chat (+ default), `nix run`, and
  nixosModules.open-chat / services.open-chat.
- Optional `nix build` CI check (Cachix).
- Docs: `nix run github:msgmate-io/open-chat-go`.

## Acceptance criteria
- `nix build .#open-chat` succeeds on x86_64-linux (aarch64-linux if feasible).
- `nix run` serves on :1984 with sqlite + embedded Redis.
- NixOS module starts a hardened unit and persists the DB.

## References
- backend/full_build.sh, .github/workflows/build.yaml, backend/server/routing.go:39
- `gh release view open-chat-staging-0.0.610`
