# Integration manager

`integrations.yaml` at the repository root is the single source of truth for
Open-Chat integrations: where their source lives, which profile selects them,
and what they contribute to the Go build and the frontend build. Integrations
are **not** git submodules; this tool fetches them on demand into the
gitignored `clients/integrations/<name>` checkouts.

## Profiles

| Profile | Default? | Integrations (plus transitive `depends_on`) |
| --- | --- | --- |
| `core-only` | ✅ | `mcp`, `rest_api_tool`, `go_client` |
| `default` | | core + `matrix`, `docker_sandbox`, `git`, `kubernetes` |
| `full` | | every integration (includes private ones) |

Select a profile with `INTEGRATION_PROFILE`, `--profile`, or the manifest's
`default_profile` (currently `core-only`).

## CLI

Run from the repository root (or pass `--repo-root`):

```bash
export PYTHONPATH=development/integrations

python3 -m openchat_integrations list    --profile core-only
python3 -m openchat_integrations sync    --profile core-only   # fetch checkouts + write lock
python3 -m openchat_integrations resolve --profile core-only   # go.work + imports_gen + tags
python3 -m openchat_integrations frontend --profile core-only  # link integration Vike packages
python3 -m openchat_integrations export  --profile core-only --dist-dir frontend/dist/client
python3 -m openchat_integrations prepare --profile core-only   # sync + resolve + frontend
python3 -m openchat_integrations check   --profile core-only   # validate lock + checkouts + pages
python3 -m openchat_integrations dev --integration git --path ../my-git-integration-fork
```

- `sync --frozen` checks out the commits pinned in `integrations.lock.json`
  instead of the manifest `ref`. CI and release builds must use `--frozen`.
- `resolve` regenerates `backend/go.work`,
  `backend/integrations/externalintegrations/imports_gen.go`, and
  `backend/.generated/build_tags`.
- `dev` writes a local override to the gitignored `integrations.local.yaml`.
  You can also set `OPENCHAT_INTEGRATION_<ID>_PATH=/path/to/checkout` directly.

## Building a specific profile

**Local development (compose)** — the `integration-sync` service runs
`prepare` before the backend starts:

```bash
INTEGRATION_PROFILE=core-only docker compose up          # default profile
INTEGRATION_PROFILE=default   docker compose up
INTEGRATION_PROFILE=full      docker compose up          # needs access to private repos
```

**Host build** (`backend/full_build.sh` calls `prepare` itself):

```bash
cd backend
INTEGRATION_PROFILE=core-only ./full_build.sh
INTEGRATION_PROFILE=default   ./full_build.sh
INTEGRATION_PROFILE=full      ./full_build.sh
```

**Production image**:

```bash
docker build --target prod-alpine \
  --build-arg INTEGRATION_PROFILE=full \
  --build-arg FRONTEND_STAGE=frontend_empty \   # skip the frontend build
  -t open-chat:full .
```

or via compose: `INTEGRATION_PROFILE=full docker compose -f docker-compose.pro.yaml build backend`.

**Android** (`clients/gomobile/build_android.sh`, default profile `full`):

```bash
INTEGRATION_PROFILE=full bash clients/gomobile/build_android.sh
```

## Frontend pages

`frontend.pages` in the manifest maps prerendered Vike output to each
integration's embedded `frontend_assets`. An integration can own this list by
shipping an `integration.frontend.json` (with `name` + `pages`), which the
exporter prefers. `frontend` links integration-provided Vike packages
(`<checkout>/frontend/package.json` + `pages/`) into the aggregator and adds
`integrations/*` to the npm workspaces.

## Reproducibility

`integrations.lock.json` pins every integration to a commit. Regenerate it with
a non-frozen `sync`; commit it. CI uses `sync --frozen` so builds are
reproducible. `check` fails when a checkout drifts from the lock.

## Private integrations

Integrations with `private: true` require authenticated git access for
`sync --frozen` (the `full` profile). CI configures a token credential via
`git config url."https://x-access-token:$TOKEN@github.com/".insteadOf`.
