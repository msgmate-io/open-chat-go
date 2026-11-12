# Development Guide

This guide covers local setup, builds, integration profiles and the public
development workflow. Operational runbooks for deployed agents and private
integrations are maintained outside this repository.

## Local development

Development should always use the development Compose stack:

```bash
docker compose build && docker compose up
```

The development server listens at `http://localhost:1984`. Run backend commands
inside the backend container when appropriate. The frontend and backend reload
automatically, so a short rebuild is usually enough to test a change. Exercise
the endpoints you implement while working on them.

Only add integration tests when asked, or when you are confident they are the
right place to pin the behaviour.

## Contributing, CI and deployment

Open feature, bugfix, docs and chore pull requests against **`dev`**, the
integration trunk - never against `main`, and never push to `dev` or `main`
directly:

```bash
git fetch origin dev
git checkout -B <branch> origin/dev
```

`main` is only advanced by the automatic `[Dev promotion]` pull request
(`dev` -> `main`); the heavy CI suites (`deploy`, `sandbox-test`, `build`,
`e2e`, `test-core-only`, `ai-test-mock`, `ai-test-real`) run on that promotion
PR only. An ordinary PR against `dev` carries the `test` label and runs the
lightweight PR CI. Release hops from `main` to `production` and the private
full-build/deployment automation are maintainer-controlled; the public workflow
files and the application code live here.

Every PR needs a line of its own, `Closes msgmate-io/open-chat-go-ci#<n>`, in
its body (`.github/PULL_REQUEST_TEMPLATE.md` already carries a `## Tracker`
section for it); `PR Test Merge Requirement` reads that and nothing else.
GitHub cannot auto-close a cross-repository issue, so the line is both the gate
input and the closure instruction. Agent-facing working notes - including the
full set of PR conventions (base, draft state, labels, tracker line, truthful
test reporting) - live in [`AGENTS.md`](AGENTS.md).

Agent coordination and operational runbooks are maintained outside this
repository. Do not add agent runbooks, private integration documentation or
deployment secrets to this repository.

## Clone the repository

The core frontend is tracked in-tree under `frontend/`. Only the public
submodules are checked out with the clone (the Go interfaces, the Python/JS
clients and the build-tools). Private profiles materialize their own checkouts on
demand during profile setup.

```bash
git clone --recurse-submodules https://github.com/msgmate-io/open-chat-go.git
# existing clone:
git submodule update --init --recursive
```

## Integrations

Integrations are declared in [`integrations.yaml`](integrations.yaml). The public
manifest lists the public integrations and the public `core-only` profile.
Additional profiles and private integrations are supplied by the deployment's
private integration source and merged in by the profile setup step.

The manager is the `openchat-integrations` CLI from
[`open-chat-go-build-tools`](https://github.com/msgmate-io/open-chat-go-build-tools),
vendored at `development/build-tools` and installed into `.venv` on first use:

```bash
python3 -m venv .venv
.venv/bin/pip install ./development/build-tools
.venv/bin/openchat-integrations list --profile core-only
.venv/bin/openchat-integrations setup --profile full
.venv/bin/openchat-integrations sync --profile full
.venv/bin/openchat-integrations resolve --profile full
.venv/bin/openchat-integrations prepare --profile full
.venv/bin/openchat-integrations export --dist-dir frontend/dist/client
.venv/bin/openchat-integrations check --profile full
```

- Profiles include the public `core-only` and `community-coder` profiles and the
  deployment-provided `default`, `full`, `full-ci`, `full-android` and
  `full-development` profiles.
- `setup` materializes a profile's extra repositories and symlinks as declared in
  [`profile_setup.yaml`](profile_setup.yaml). It runs automatically before other
  commands when the profile setup marker is missing or stale.
- `sync --frozen` checks out the commits pinned in `integrations.lock.json` and
  is used by CI and release builds. Private pins are kept in the deployment's
  private lock, never in the public lockfile.
- Local integration development:
  `.venv/bin/openchat-integrations dev --integration git --path ../my-fork`, or
  set `OPENCHAT_INTEGRATION_<ID>_PATH=/path/to/checkout`.
- The generated Go workspace lives at `backend/go.work` (gitignored). All
  modules resolve through it; `backend/go.mod` does not hardcode integrations.
- `docker compose up` runs an `integration-sync` init service that performs
  `prepare` before the backend starts.
- Private checkouts need credentials. The manager can discover host git
  credentials; as a fallback, add a token once with
  `echo "GITHUB_TOKEN=$(gh auth token)" >> .env`.
- Full reference:
  [`development/build-tools/README.md`](development/build-tools/README.md).

### Building a specific integration profile

| Profile | Includes | Extra repos |
| --- | --- | --- |
| `core-only` (default) | `mcp`, `rest_api_tool`, `go_client`, `community_coder` | – |
| `community-coder` | public coder + base integrations (the private `tui` client is added by the private `default` profile) | – |
| `default` | core integrations plus deployment-provided integrations | profile setup |
| `full` | every integration | profile setup |
| `full-ci` | every integration plus private CI tooling | profile setup |
| `full-android` | full profile plus the mobile client | profile setup |

```bash
# Local development (Compose runs the integration-sync service for you)
INTEGRATION_PROFILE=core-only docker compose up
INTEGRATION_PROFILE=full      docker compose up

# Host build
cd backend
INTEGRATION_PROFILE=core-only ./full_build.sh
INTEGRATION_PROFILE=full      ./full_build.sh

# Production image
INTEGRATION_PROFILE=full docker compose -f docker-compose.pro.yaml build backend
# or directly:
docker build --target prod-alpine --build-arg INTEGRATION_PROFILE=full -t open-chat:full .

# Android
INTEGRATION_PROFILE=full-android bash clients/gomobile/build_android.sh
```

## Running the development stack on PostgreSQL

The development stack uses SQLite by default. PostgreSQL is available behind the
`postgres` Compose profile:

```bash
docker compose -f docker-compose.yaml -f docker-compose.postgres.yaml \
  --profile postgres up
```

To make it the default for your shell (still SQLite when unset):

```bash
export COMPOSE_FILE=docker-compose.yaml:docker-compose.postgres.yaml
export COMPOSE_PROFILES=postgres
docker compose up
```

PostgreSQL listens on host port `5433`, persists to the `postgres_data` volume,
and uses `openchat:openchat` by default. Override with `POSTGRES_USER`,
`POSTGRES_PASSWORD`, `POSTGRES_DB` or a `DB_PATH`/`DB_BACKEND=postgres`
connection string.

Both backends must behave identically. In particular, JSON columns should use
`json.RawMessage`/`[]byte`, or `database.JSONRaw` for JSON the core writes
directly, so PostgreSQL does not encode parameters as `bytea`.

## Switching branches after a dev build

`docker compose up` can regenerate two tracked files:

- `integrations.lock.json` — a non-frozen integration sync re-pins it to the
  current checkouts;
- `backend/server/swagger.json` — refreshed by every backend rebuild.

The dev tooling restores both automatically. If an older checkout left them
dirty, run:

```bash
development/scripts/restore_generated_artifacts.sh
```

## Custom integration profiles

Where things live:

- `integrations.yaml` (public): public integrations and the `core-only` profile.
- The deployment's private integration source: private integrations and the
  private profiles. Setup mirrors its manifest into `.integrations/private/`
  (gitignored).
- `profile_setup.yaml` (public): per profile, the extra repositories and symlinks
  that setup materializes. It names repository locations, never source.

Adding a profile:

1. Add an entry to `profiles:` and, for a new integration, to `integrations:` in
   the public manifest or the deployment's private fragment.
2. If the profile needs extra repositories or symlinks, add them under `repos:`
   and the profile's `repos:`/`symlinks:` in `profile_setup.yaml`.
3. Run `openchat-integrations setup --profile <name>`, then
   `sync`/`resolve`/`frontend`.

Useful flags:

- `setup --repo <id>` materializes a single extra repository.
- `--no-setup` skips automatic setup; `setup --force-setup` re-runs it.
- `OPENCHAT_NO_SETUP=1` skips setup inside images that are already prepared.

Private pins live in the deployment's private lockfile, never in
`integrations.lock.json`. Refresh pins with
`openchat-integrations sync --profile <profile>` after pushing integration
changes.

## Integration frontend pages

Frontend pages for integrations are implemented inside each integration
repository under `frontend/pages/...` (React, Vike and `@open-chat-go/ui` using
`IntegrationPageShell`). The in-repo `frontend/` owns only the shared component
contract and the generic integrations overview page.

1. Write pages in the integration repository at `frontend/pages/`.
2. Declare prerendered pages in `integration.frontend.json` at the integration
   root (`pages: [{source, asset}]`). The public manifest only lists the publish
   name, so private page routes do not leak.
3. Run `openchat-integrations frontend` (or `prepare`) to link the page sets
   into `frontend/pages/integrations/<name>`.
4. Run `development/scripts/build_static_frontend.sh` (or the Dockerfile) to
   build and export the pages with `openchat-integrations export`.
5. Embed `frontend_assets` in the integration Go code and register the pages in
   `integrationinterface.Definition.FrontendPages`.

### Hot reload

- Re-run `openchat-integrations frontend --profile <profile>` whenever a page
  set is added or renamed, then restart the Vite dev server.
- The in-repo frontend scripts set `VIKE_CRAWL='{"git":false}'`, so gitignored
  page-set symlinks are discovered on every host.
- Inside the dev Compose stack this happens automatically at container start.
- `frontend/integrations/extensions.gen.ts` is generated (gitignored).
  `frontend/scripts/ensure-extensions-gen.mjs` creates an empty placeholder
  before install/dev/build.

## Dependency-surface baseline

`integrations.dep-surface.json` records integrations whose shipped packages no
longer import `backend/...`. `development/scripts/check_dep_surface_gate.sh`
validates the baseline without a Go toolchain and, when a workspace exists, fails
only on a regression against it.

The baseline should stay small. Extend it only in its own commit, for an
integration that is fully decoupled:

```bash
python3 backend/scripts/integration_dep_surface.py --write-baseline integrations.dep-surface.json
python3 backend/scripts/integration_dep_surface.py --check-baseline integrations.dep-surface.json
git add integrations.dep-surface.json
```

A coupled integration listed there is a regression by definition; fix the import
instead of re-baselining.

## Documentation lookup index

User- and developer-facing documentation lives in the frontend docs pages at
`frontend/pages/docs/content/*.mdx` and is published at
[`msgmate.io/docs`](https://msgmate.io/docs). Useful pages:

- [`development.mdx`](frontend/pages/docs/content/development.mdx) — local setup.
- [`integration-development.mdx`](frontend/pages/docs/content/integration-development.mdx) — integration/tool interfaces, profiles, frontend pages and adding a custom integration.
- [`integrations.mdx`](frontend/pages/docs/content/integrations.mdx) — integration registry, tools and access model.
- [`self-hosting.mdx`](frontend/pages/docs/content/self-hosting.mdx) — deployment and operations.

Also see [`development/build-tools/README.md`](development/build-tools/README.md)
for the `openchat-integrations` CLI.
