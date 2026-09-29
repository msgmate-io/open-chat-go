### General Development Guide

Development should always use the development compose via `docker compose build && docker compose up` (*likely the development compose is already running!*).
Use the development server backend at `http://localhost:1984` and run commands inside the backend container if needed.

The frontend and backend code automatically reloads in the backend container, so after a short rebuild and reload time you can directly test features yourself.
You should always try to call and test endpoints you implement while you are implementing them and check that they are working.

You should only implement actual integration tests if asked, or suggest to implement them if you really think appropriate.

### Clone the repository

The core frontend is tracked in-tree under `frontend/` (no longer a git
submodule). Only the public submodules are checked out with the clone (the Go
interfaces, the Python/JS clients and the build-tools). Private repositories
(the private integrations manifest / CI, the Helm chart, the mobile client and
the LLM coding context) are materialized on demand by the profile setup step.

```bash
git clone --recurse-submodules https://github.com/msgmate-io/open-chat-go.git
# existing clone:
git submodule update --init --recursive
```

### Integration manager

Integrations are declared in [`integrations.yaml`](../integrations.yaml) at the
repo root. The public manifest lists the public integrations and the
`core-only` profile; private integrations and the private profiles come from a
private fragment that is materialized by `setup`. The manager is the
`openchat-integrations` CLI from
[`open-chat-go-build-tools`](https://github.com/msgmate-io/open-chat-go-build-tools),
vendored at `development/build-tools` and installed into `.venv` on first use:

```bash
python3 -m venv .venv
.venv/bin/pip install ./development/build-tools   # or git+https://github.com/msgmate-io/open-chat-go-build-tools.git
.venv/bin/openchat-integrations list --profile core-only
.venv/bin/openchat-integrations setup --profile full          # repos + symlinks + private manifest
.venv/bin/openchat-integrations sync --profile full           # fetch checkouts + lock
.venv/bin/openchat-integrations resolve --profile full        # go.work + imports_gen + tags
.venv/bin/openchat-integrations prepare --profile full        # sync + resolve + frontend
.venv/bin/openchat-integrations export --dist-dir frontend/dist/client
.venv/bin/openchat-integrations check --profile full
```

- Profiles: `core-only` (default), `default`, `full`, `full-ci` and
  `full-android`. Override with `INTEGRATION_PROFILE`.
- `setup` materializes the profile's extra repositories and symlinks as
  declared in [`profile_setup.yaml`](../profile_setup.yaml). It runs
  automatically before any other command when the profile's setup marker is
  missing or stale, so a build never runs against a half-configured workspace.
- The private integrations manifest + lockfile live in the private `ci`
  repository; `setup` mirrors them into `.integrations/private/` (gitignored).
  The public `integrations.lock.json` never contains private pins.
- `sync --frozen` checks out the commits pinned in `integrations.lock.json`
  (used by CI and release builds). Private pins come from the private lock.
- Local integration development: `.venv/bin/openchat-integrations dev --integration git --path ../my-fork`,
  or set `OPENCHAT_INTEGRATION_<ID>_PATH=/path/to/checkout`.
- The generated Go workspace lives at `backend/go.work` (gitignored). All
  msgmate modules resolve through it; `backend/go.mod` no longer hardcodes
  integrations.
- `docker compose up` runs an `integration-sync` init service that performs
  `prepare` before the backend starts (and therefore also `setup`).
- Private profiles (`default`, `full`, …) clone private repositories from the
  `integration-sync` container. The manager discovers the host credentials and
  configures git for those clones: `GITHUB_TOKEN`/`GH_TOKEN`, the host `gh`
  config, `~/.git-credentials` or a forwarded SSH agent. The dev compose mounts
  the host home read-only and forwards `SSH_AUTH_SOCK` for this. If your `gh`
  token lives in the OS keyring (not `hosts.yml`), add it to `.env` once:
  `echo "GITHUB_TOKEN=$(gh auth token)" >> .env`.
- Full reference: [`development/build-tools/README.md`](../development/build-tools/README.md).

### Building a specific integration profile

| Profile | Includes | Extra repos |
| --- | --- | --- |
| `core-only` (default) | `mcp`, `rest_api_tool`, `go_client` | – |
| `default` | core + `matrix`, `docker_sandbox`, `git`, `kubernetes` | private manifest fragment |
| `full` | every integration | private manifest fragment |
| `full-ci` | every integration | full private CI tooling + Helm chart |
| `full-android` | every integration | private CI tooling + mobile client |

```bash
# Local development (compose runs the integration-sync service for you)
INTEGRATION_PROFILE=core-only docker compose up
INTEGRATION_PROFILE=full      docker compose up   # needs access to private repos

# Host build (full_build.sh runs `prepare` itself)
cd backend
INTEGRATION_PROFILE=core-only ./full_build.sh
INTEGRATION_PROFILE=full      ./full_build.sh

# Production image
INTEGRATION_PROFILE=full docker compose -f docker-compose.pro.yaml build backend
# or directly:
docker build --target prod-alpine --build-arg INTEGRATION_PROFILE=full -t open-chat:full .

# Android (uses the mobile client from the full-android profile)
INTEGRATION_PROFILE=full-android bash clients/gomobile/build_android.sh
```

`sync --frozen` (used by CI/release) checks out the commits pinned in
`integrations.lock.json`; `check` verifies the checkouts still match the lock.

### Custom integration profiles

Profiles let a checkout compile and serve exactly the integrations (and extra
private repositories) a build needs, while the public repository stays free of
private source.

Where things live:

- `integrations.yaml` (public): the public integrations and the public
  `core-only` profile.
- the private `ci` repository `openchat/integrations.private.yaml`: the private
  integrations plus the `default`, `full`, `full-ci` and `full-android`
  profiles. `setup` mirrors it into `.integrations/private/` (gitignored).
- `profile_setup.yaml` (public): per profile, the extra repositories and
  symlinks that `openchat-integrations setup` materializes. It only names
  repository **locations**, never source.

Adding a profile:

1. Add an entry to `profiles:` and (for a new integration) to `integrations:`
   in `integrations.yaml` (public) or in the private fragment (private).
2. If the profile needs extra repositories or symlinks, add them under
   `repos:` and the profile's `repos:`/`symlinks:` in `profile_setup.yaml`.
3. Run `openchat-integrations setup --profile <name>` (or any other command,
   which triggers setup automatically), then `sync`/`resolve`/`frontend`.

Useful flags:

- `setup --repo <id>` materializes a single extra repo (e.g.
  `llm_coding_agents`) without a profile.
- `--no-setup` skips the automatic setup; `setup --force-setup` re-runs it.
- `OPENCHAT_NO_SETUP=1` skips setup entirely (used inside build images that
  already received the prepared context).

Lockfile rules: private integration pins live in the private
`ci` repository lockfile (`openchat/integrations.private.lock.json`), never in
the public `integrations.lock.json`. Refresh pins with
`openchat-integrations sync --profile <profile>` after pushing integration
changes.

### Integration Frontend Pages Development

Frontend pages for integrations are implemented **inside each integration
repository** under `frontend/pages/...` (built with React, Vike, and
`@open-chat-go/ui` using `IntegrationPageShell`). The in-repo `frontend/`
directory owns only the shared component contract (`@open-chat-go/ui`) and the
generic integrations overview page.

#### Build & Export Workflow:
1. **Source Pages**: Write pages in the integration repo at `frontend/pages/` (e.g. `sandboxes/+Page.tsx`, `sandboxes/add/+Page.tsx`).
2. **Page Map**: Declare the prerendered pages in `integration.frontend.json` at the integration repo root (`pages: [{source, asset}]`). The public `integrations.yaml` only lists the publish name, so closed-source page routes do not leak.
3. **Linking**: `openchat-integrations frontend` symlinks each page set into the aggregator at `frontend/pages/integrations/<name>` for the selected profile (necessarily re-run it in the integration checkout, it also anchors node_modules). It runs automatically at container start and image build.
4. **Prerendering + Export**: `development/scripts/build_static_frontend.sh` (or the Dockerfile) runs `npm run build` then `openchat-integrations export` copies the prerendered HTML to the integration's `frontend_assets/`.
5. **Integration Registration**: The integration Go code embeds `frontend_assets` via `//go:embed frontend_assets` and registers the pages in `integrationinterface.Definition.FrontendPages` so the OpenChat backend serves them automatically.

### Chat extensions (cross-integration chat UI)

Chat extension registrations (message inputs, details views, pre-start selectors) live in the integration repo too. Each page set declares an optional `"extension": "<entry-file>"` in `integration.frontend.json`; when linked, the manager regenerates `frontend/integrations/extensions.gen.ts`, which the public app imports at startup so registrations happen globally (the chat page does not import integration code directly). Cross-integration dependencies must go through the registry lookup (`resolveChatUIExtension("opencode")`), never through cross-repo relative imports.

#### Local dev / hot-reload loop

- Run `openchat-integrations frontend --profile <profile>` (or the higher-level `prepare`); it creates the page-set symlinks and the per-checkout `frontend/node_modules` anchor automatically. Re-run it whenever a page set is added or renamed, then restart the Vite dev server.
- The in-repo `frontend/` `npm run dev` / `npm run build` scripts already set `VIKE_CRAWL='{"git":false}'`, so gitignored page-set symlinks are discovered on every host; do not remove that env prefix.
- Inside the dev compose this happens automatically at container start (`integration-sync` service).
- Note: under Docker, container-side builds own `frontend/node_modules/.vite`; when building the frontend on the host afterwards, `chown` that directory first.

### Documentation lookup index

The user- and developer-facing documentation lives in the **frontend** docs
pages at `frontend/pages/docs/content/*.mdx` and is published at
[`msgmate.io/docs`](https://msgmate.io/docs) (each file is a page selected by
its `path:` frontmatter). Read them for further instructions before changing an
area they cover:

- [`development.mdx`](../frontend/pages/docs/content/development.mdx) — local development setup.
- [`integration-development.mdx`](../frontend/pages/docs/content/integration-development.mdx) — integration/tool interfaces, profiles, frontend page linking and **adding a custom integration**.
- [`integrations.mdx`](../frontend/pages/docs/content/integrations.mdx) — integration registry, tools and access model.
- [`self-hosting.mdx`](../frontend/pages/docs/content/self-hosting.mdx) — deployment and operations.

Also useful:

- [Build tools README](../development/build-tools/README.md) — the `openchat-integrations` CLI.
