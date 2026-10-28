# ci#423 — Decouple integrations behind a stable Host interface (2026-10-28)

## Verdict: delivered in two phases (interface module + monorepo)

- **Interface module** (`clients/go_integration_interface`, upstream repo
  `msgmate-io/open-chat-go-integration-interface`, PRs #4/#5): the stable,
  versioned `Host` surface — config reads (HostVersion 1), request-scoped and
  process-wide storage, HTTP (incl. private-DB variant), chat/workspace/web/
  tokens/tools capabilities (HostVersion 3), and runtime-config writes + local
  accounts (HostVersion 4, `HostVersion = 4`). Guarded so `fs.FS` /
  `http.ServeMux` / `gorm.DB` stay out of the interface surface
  (`development/scripts/check_host_surface.py`).
- **Monorepo** (`msgmate-io/open-chat-go`, this PR): the core adapter
  `backend/integrationhost` satisfies the Host from the concrete core types and is
  installed at server/worker startup; the `go-client-integration` and
  `rest-api-tool-integration` repos were migrated off `backend/…` imports onto the
  Host (each has `backend: 0` in `integrations.dep-surface.json`), while the
  `mcp-integration` repo (sibling PR #10) stays coupled pending its in-repo
  migration.

## In this PR

- `backend/integrationhost` — thin adapter implementing the stable Host surface
  (`host.go`, `capabilities.go`, `platform.go`); installed via
  `integrationhost.Install()` in `backend/cmd/server.go` and
  `backend/cmd/worker.go`; `contract_test.go` + `host_test.go` exercise the
  installed Host against the concrete runtime config and open the storage/HTTP
  surfaces.
- Integration host wiring self-check: `backend/cmd/integration_host_wiring_test.go`.
- Decoupling/dep-surface guards, wired into `integration-module-ci.yaml`:
  - `integrations.dep-surface.json` — baseline (go_client and rest_api_tool
    decoupled at `backend: 0`; mcp still migrating, not pinned);
  - `integrations.host-surface.json` — Host surface pin (HostVersion 4, remaining
    pinned leaks = migration backlog);
  - `development/scripts/check_dep_surface_gate.sh`,
    `development/scripts/check_integration_decoupling.py` (+ tests) and
    `development/scripts/check_host_surface.py` (+ tests), run in CI.
- `development/scripts/integration_module_ci.sh` — runs the guards on the
  materialized workspace, then `go test ./...` on `clients/integrations/...`
  (test-only `backend/…` imports stay legal while migration is incremental).

## Migration recipe for remaining integrations (mcp → slack/matrix/signal/git)

1. Replace direct `backend/…` imports with `integrationinterface.RequireHost()`
   (config reads/logging via `Host.Config()/Logger()`, DB via the opaque
   `Host.Storage().DB()` handle, HTTP via `Host.HTTP()`).
2. Interfaces first, then the adapter: a package in `backend/integrationhost`
   written against the concrete core types with a compile-time
   `var _ integrationinterface.X = (*adapter)(nil)` assertion is the template.
3. Where a capability is missing, extend the interface module (bump
   `HostVersion`) and the adapter, then shrink the pinned leaks and re-pin
   `integrations.host-surface.json` via
   `python3 development/scripts/check_host_surface.py --write-baseline`.
4. Remove the `replace backend => …` directive, run
   `GOWORK=off python3 development/scripts/check_integration_decoupling.py --module-dir .`
   until it exits 0, then re-pin the module in `integrations.lock.json` and
   `integrations.dep-surface.json`.

## Pending (next ticket, not this PR)

- Migrating the `mcp-integration` repo off its `replace backend => …` directive
  (currently coupled; `integrations.dep-surface.json` intentionally does not pin
  it yet).
- Re-pinning the mcp lock entry once that lands; the guard then walks the
  remaining integrations (slack/matrix/signal/git are private).
