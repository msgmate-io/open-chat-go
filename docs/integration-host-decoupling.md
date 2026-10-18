# Decoupling integrations behind the stable Host interface (ci#423)

Status: design + first slice plan. The Host surface itself (`HostVersion 4`) is
implemented in `clients/go_integration_interface/integrationinterface` and
adapted by `backend/integrationhost`; the user-facing recipe lives in
`frontend/pages/docs/content/integration-host-migration.mdx`.

Goal (prereq for per-integration prebuilds): an integration module must compile
from **interface module + tool-interface module + its own sources only** — no
`backend/...` import in its dependency graph, so it can be prebuilt once per
commit instead of once per core change.

## Rules

1. Interface types are ORM/framework free (`backend/integrationhost` owns
   `*gorm.DB`, never the interface module).
2. Only add a capability when at least one integration needs it; never add
   integration-specific methods. No `fs.FS`, `http.ServeMux`, `gorm.DB`.
3. Additive change => bump `HostVersion` and append to the version history;
   integrations gate on `host.Version()` and may degrade when too old.
4. Anything that must run in `init()` (before the Host is installed) is a
   package-level registry in the interface module
   (`RegisterChatBackend`, `RegisterSecretProvider`, `Register`), drained by
   `integrationhost.Install()`.
5. The adapter is thin: live lookups, no caching, errors translated so the
   integration can `errors.Is(err, integrationinterface.ErrAuthorizationRequired)`.

## Remaining core seams (slice queue)

| Integration | Still imports | Needed capability |
| --- | --- | --- |
| tvault | `backend/secretstore`, `backend/database`, `backend/server/util` | `Secrets()` (new), `Storage()`/`HTTP()` |

`Secrets()` (planned `HostVersion 5`) closes the first seam: an integration
becomes a *provider* of seal/unseal instead of importing the core registry.

```go
type Secrets interface {
    Seal(ctx, SealRequest) (SecretBlock, error)
    Unseal(ctx, UnsealRequest) ([]byte, error)
    Authorize(ctx, AuthRequest) (AuthStatus, error)
    Status(ctx, AuthRequest) (AuthStatus, bool)
    Revoke(ctx, AuthRequest)
    NewRef() (string, error)
    ActiveProvider() string
    Providers() []string
}
```

`UnsealRequest{Block, SessionKey}` carries the authorization cache key
explicitly, so the interface needs no `context` session helpers from the core.
`RegisterSecretProvider(SecretProvider)` mirrors `RegisterChatBackend`: init
time, Host-free; the adapter bridges each registered provider into the core
`secretstore` registry.

## Slices

1. **This PR**: design + rules above (docs only, no behaviour change).
2. `HostVersion 5` `Secrets()`: interface types + registry, `integrationhost`
   adapter/bridge, conformance test with a fake provider.
3. Migrate tvault off `backend/secretstore` (+ `RegisterSecretProvider`), then
   off `backend/database` via `Storage()`/`HTTP()`.
4. Enforce: a dep-surface gate that fails when a synced integration's module
   graph contains `backend/...`; then per-integration prebuilds.

## Non-goals

No `backend/...` type re-exports in the interface, no big-bang migration, no
per-integration prebuild pipeline in this slice.
