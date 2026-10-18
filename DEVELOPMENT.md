### General Development Guide

Development should always use the development compose via `docker compose build && docker compose up` (*likely the development compose is already running!*).
Use the development server backend at `http://localhost:1984` and run commands inside the backend container if needed.

The frontend and backend code automatically reloads in the backend container, so after a short rebuild and reload time you can directly test features yourself.
You should always try to call and test endpoints you implement while you are implementing them and check that they are working.

You should only implement actual integration tests if asked, or suggest to implement them if you really think appropriate.

### Pull-request CI labels and deployment policy

PR CI (`.github/workflows/run-pr-ci-tests-and-deployments.yaml`) is driven
entirely by labels applied to the pull request when it is opened: `test`,
`build`, `e2e`, `sandbox-test`, `test-core-only`, `ai-test-mock`,
`ai-test-real` and `deploy` (`release-android` only on an explicit Android
request; `slow-android-build-test` only on an explicit maintainer request).
Two of them stand apart: `deploy` and `sandbox-test` spin up a deployment and
run the expensive post-deploy suites, so they touch an environment.

Policy:

- Use `deploy` and `sandbox-test` **only on dedicated consolidation PRs
  targeting `main`** (the `chore/consolidate-*` PRs) — the single place where a
  batch of ready changes is verified as a whole. Feature, bugfix, docs and
  chore PRs carry the ordinary code-change labels only.
- A same-repository PR targeting `main` is the CI-environment case: its
  deploy/test gate uses the **unprotected** `msgmate-io-ci` GitHub environment,
  so the full suite — including the post-deploy sandbox and real-AI suites —
  runs without an admin confirmation.
- Every other PR keeps the protected `msgmate-io-pr-deployment` environment and
  therefore needs an explicit maintainer approval before it deploys (and before
  its post-deploy suites can run).
- Production promotion (`main` → `production`) always keeps the
  admin-protected `msgmate-io-production` environment.
- Maintainers may make rare, deliberate exceptions (for example to validate an
  infrastructure change); the risk is theirs and they should say so on the PR.

Never wait for or poll CI, deployments or approvals from an agent turn; report
what is pending instead.

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

### Native coding-agent sandboxes

The native `msgmate_coder` coding agents can run each chat in a **fully
isolated, per-chat coding sandbox** instead of a shared bootstrap workspace.
The chat-init `coder_select_workspace` `auto_sandbox` option provisions the
sandbox through the sandbox integration, which owns the Docker and Kubernetes
backends; on Kubernetes it is an ephemeral privileged pod with its own
persistent workspace and state PVCs, so the checkout survives restarts and stays
**continuable** across messages for its TTL. A periodic reaper tears the backing
pod and PVCs down when the sandbox expires.

The sandbox target is selectable three ways, per field, with precedence
**chat-init input → per-agent shared config → deployment env**:

- chat-init: `sandbox_backend`, `sandbox_template`, `sandbox_image`,
  `sandbox_workspace_size`;
- per-agent shared config: `coder_sandbox_backend`, `coder_sandbox_template`,
  `coder_sandbox_image`, `coder_sandbox_workspace_size`;
- deployment env: `OCI_MSGMATE_CODER_TEMP_SANDBOX_BACKEND` and its
  `_TEMPLATE` / `_IMAGE` / `_WORKSPACE_SIZE` counterparts.

When no backend is configured (or no sandbox integration is present), the
legacy first-enabled-SSH-server behavior is kept, so existing deployments are
unaffected. The OpenCode-based coding bots remain the default/backup route and
are not modified by the native sandbox flow.

The capability parity of the native coder against the OpenCode bot suite (per
bot, per capability, with the explicitly accepted fallbacks) is recorded in
[`docs/native-coder-parity.md`](../docs/native-coder-parity.md).

### Native coding-agent provider concurrency control

Under full-throttle development (many native agents at once) a shared provider
key (typically OpenRouter) answers **HTTP 402 "This request would exceed your
available credits given your current in-flight requests"** — a per-key
*in-flight* cap, not an account-credit cap (ci#620). The native `msgmate_coder`
runtime therefore admits every provider request through a **per-key in-flight
limit**: additional requests queue (up to the queue timeout) and succeed instead
of 402ing, the current in-flight/queue counts are exposed, requests over the cap
that *do* get rejected with 402/429 fail over to a configured fallback
provider/secondary key, and the runtime keeps metrics (in-flight, 402/429 rate,
queue wait, saturation events) so the development loop can self-throttle.

The default in-flight cap is 3 — the development-loop controller's
`MAX_CONCURRENT` default (ci#592) — and is bounded by the pooled sandbox bound
`OCI_MSGMATE_CODER_MAX_AGENTS_PER_POOL` (ci#602) when that is smaller. The
per-agent keys (`coder_provider_max_concurrency`,
`coder_provider_queue_timeout_ms`, `coder_provider_fallback_on_saturation`,
`coder_provider_fallback_backend`/`_model`/`_endpoint`/`_api_key_env`,
`coder_provider_fallbacks`) mirror the deployment env
(`OCI_MSGMATE_CODER_PROVIDER_MAX_CONCURRENCY`, `..._QUEUE_TIMEOUT_MS`,
`..._FALLBACK_ON_SATURATION`, `..._FALLBACKS`). A fallback of a known provider
with no explicit key env resolves `<PROVIDER_API_KEY>_FALLBACK`, so an operator
only has to export one extra variable to shed load onto a second key. No key —
primary or secondary — is ever exposed: the admission identity and every status
snapshot carry only a truncated hash. `coder_context` reports the in-flight
count and cap, and the admin-only
`GET /api/v1/integrations/msgmate_coder/provider/status` route reports the
per-key metrics for the loop. The full reference lives in the
`msgmate_coder` integration README.

### Native coding-agent auto-consolidation (default ON)

A native `msgmate_coder` agent consolidates its **own ready pull requests by
default** (ci#653): it no longer depends on the scheduled loop controller or a
human to roll a batch up. The behaviour is driven by the `auto_consolidation`
block of the agent's shared config, which the core merges into every config that
declares a native coder agent (a `coder_*` role/sandbox/provider key, a `coder_*`
tool, or a `coder_select_workspace` tool_init entry) instead of leaving it to
each agent (or bot template) to opt in:

```yaml
auto_consolidation:
  enabled: true            # default ON
  threshold: 1             # consolidate as soon as >= 1 PR is ready
  base: main
  scope: repo              # only this repo's PRs (optionally `repos: [owner/name]`)
  authors: [self]          # only our bot's PRs unless configured otherwise
  require_green: true      # never include failing/conflicting/draft PRs
  labels: [test, build, e2e, test-core-only, ai-test-mock, ai-test-real, sandbox-test, deploy]
  branch_prefix: chore/consolidate
  one_open_at_a_time: true # new PRs roll into the NEXT consolidation (ci#652)
  rollup_issue: true       # maintain a consolidation tracker issue (ci#415)
```

The block is merged at the same two places the integration shared-config defaults
are: when a bot's `default_shared_config` is written/updated (and defaults are
applied to an interaction's effective config) and when a chat is created. Both
run the integration defaults and then the coder default, so it applies in a
full build and in `core-only`, where no `msgmate_coder` integration is compiled
in.

Behaviour (at the shared interaction-completion choke point, next to the
auto-continuation loop of ci#537 and the post-interaction hook of ci#649):

1. detect the **ready** open PRs in scope — green checks, targeting `main`,
   non-draft, mergeable, authored by the agent;
2. open **exactly one** `chore/consolidate-<date>` PR that folds them in, or
   refresh the one that is already open, and apply the standard label set;
3. merge it through the existing auto-approve path (ci#538) once every required
   check is green;
4. keep exactly one consolidation PR open: later ready PRs roll into the **next**
   consolidation (ci#652), and `rollup_issue` keeps the tracker issue (ci#415)
   current.

The pass runs only when a GitHub credential (`OPEN_CHAT_GITHUB_POST_TOKEN`,
`GITHUB_TOKEN` or `GH_TOKEN`) and a repository (`OPEN_CHAT_GITHUB_REPO`, or the
per-agent `auto_consolidation_repo` key) are configured; without them it is a
no-op, so a stock deployment is unaffected. The per-chat pass is rate-limited to
one run per minute so an auto-continued interaction does not re-run it on every
turn.

Toggle and rollback (three levels, most specific wins):

- per agent: `"auto_consolidation": {"enabled": false}` in the bot's
  `default_shared_config` (or `require_confirmation`-style config delta);
- per fleet: `OCI_MSGMATE_CODER_AUTO_CONSOLIDATION=0` (also `false`/`off`/`no`/
  `disabled`) suppresses the default for every agent that does not set the key
  explicitly;
- `auto_consolidation: {"threshold": N, "authors": ["*"], "one_open_at_a_time":
  false, ...}` narrows or widens any other field for a single agent.

`deploy` and `sandbox-test` appear in the label set **only** because a
consolidation PR targeting `main` is the single place where a batch of ready
changes is verified as a whole (see "Pull-request CI labels and deployment
policy" above); feature/bugfix/chore PRs keep the ordinary code-change labels.

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
- The `integration-sync` service repairs and then drops privileges to the owner of the bind-mounted checkout, so the generated integration clones, `.integrations` cache/markers and frontend symlinks stay owned by you (git no longer reports them as "dubious ownership"). If an older run left them root-owned, the next `docker compose up` heals them; `sudo development/scripts/fix_dev_permissions.sh` does the same without starting the stack.
- `frontend/integrations/extensions.gen.ts` is generated (gitignored). `frontend/scripts/ensure-extensions-gen.mjs` creates an empty placeholder before `npm run dev`/`build`/`install`, and the manager overwrites it with the selected profile's extension imports.
- Note: the `frontend`/`storybook` containers still run as root, so they own `frontend/node_modules` and `frontend/node_modules/.vite`; when building the frontend on the host afterwards, `chown` that directory first.

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
