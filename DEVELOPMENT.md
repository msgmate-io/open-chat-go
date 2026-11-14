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

### The promotion hop is agent-closable, and verifying it is the price

Merging the `[Dev promotion]` pull request is what deploys ci.msgmate.io, so
while that merge stayed a maintainer click the loop could never close itself:
tickets piled up as "implemented" but never "live" (ci#1055). The development
lead may therefore merge the promotion PR on its own once **all required checks
on it are green** - and must then verify that the deployment actually serves the
promoted code. The two decisions are implemented once, as unit-tested
predicates, in `development/scripts/promotion_hop.py`:

```bash
development/scripts/promotion_hop.sh merge                 # may the hop be merged now?
development/scripts/promotion_hop.sh deploy <VERSION>      # is that version live?
```

`merge` exits `0` merge, `1` wait (green not reached), `2` refuse (the PR is not
the `dev` -> `main` hop, is a draft, has conflicts, or a required check really
failed). Do not re-derive the rules from raw API payloads: the direct cause of
the false-blocked promotion PRs was a *superseded* duplicate check-run - a
`labeled` event spawns a second `pull_request` dispatch for the same SHA, the
duplicate is cancelled by the shared `pr-ci-<pr>-<sha>` concurrency group, and
its `always()` gate carries on to leave a FAILURE `PR Test Merge Requirement`
on the commit while the live run's check of the same name goes green. The
predicate collapses the check list onto the **newest** instance per
(name, app) - the same rule the auto-approve gate applies (ci#847) - and lists
every instance it disregarded as `ignored superseded check-run instances`, so a
refusal is never based on a stale red and a real red is never whitewashed.

`deploy` treats a merge as *not* acceptance. It fails unless the rollout
completed, every pod is ready, and `https://ci.msgmate.io/api/v1/metrics/health`
answers `200` with `{"status":"ok","version":"<VERSION>"}`. A host serving an
older version - or one that only exists on unmerged branches (ci#890) - is a
broken deployment, not a note: report it on the ticket and loop, do not close the
ticket. A ticket with a behavioural criterion closes only after its feature has
been observed working on ci.msgmate.io; a green promotion PR and a successful
rollout are necessary, never sufficient.

### "Green" is not "mergeable" (ci#1066)

A fully green promotion PR can still be unmergeable, and that is not a
regression: the `main` ruleset requires **one approving review from a reviewer
with write access**. A `github.token` review does not count (it is not a
write-access reviewer) and the promotion PR's author cannot approve its own PR,
so without a configured approver identity the hop needs a maintainer merge even
though every check is green.

`auto-approve-consolidation.yaml` therefore *classifies* the block instead of
failing on it, and a red auto-approve run keeps meaning "something is really
broken":

| Credential state | Behaviour |
| --- | --- |
| `CONSOLIDATE_APPROVER_TOKEN` unset, identity unresolvable | `token_missing`: warning, the review call stays authoritative |
| token rejected by the API (`401` / no `/user`) | `token_rejected`: noticed, PRs reported as awaiting a maintainer merge, run stays green |
| token accepted but `repos.permissions.push` is not `true` | `token_unprivileged`: noticed, same awaiting-report, run stays green |
| token accepted with write access | approval posted; a ruleset refusal afterwards is classified, never retried |

On the `dev` -> `main` hop only, such a block also emits a `::notice::` and
**one** idempotent PR comment stating that the PR is green and awaiting a
maintainer merge. Any other merge failure - an unreadable PR, conflicts, a base
branch modified mid-flight - still fails the run.

To restore the unattended hop, either issue `CONSOLIDATE_APPROVER_TOKEN` for an
identity with write access to this repository, or register a ruleset bypass actor
for the automation identity. Both are security configuration decisions and stay
with the maintainer.

**A green hop is not proof the approval path works.** While
`Main (Staging) Merge Protection` (rulesets `26947244` / `26947249`) has
`enforcement` other than `active` - i.e. no branch ruleset matches
`refs/heads/main`, `rules/branches/main` answers `[]` - the review requirement is
**not enforced**, so an armed `--auto` merge lands immediately and even before the
required check has started. That state *masks* the defect above instead of fixing
it: re-enabling the ruleset brings the original failure straight back. Never
enable the ruleset first and then hope the approval works; the order is approver
identity (or bypass actor) first, ruleset `active` second.

`auto-approve-consolidation.yaml` therefore runs a read-only
`Preflight main merge protection (ci#1066)` step before anything merges and
reports the live protection state in the job summary:

| State | Meaning |
| --- | --- |
| `review_required_enforced` | the `main` ruleset requires *N* approving reviews: the hop needs the approver identity / bypass actor, otherwise it awaits a maintainer merge |
| `review_required_masked` | no approving review is enforced: the unattended hop works only because the gate is off - warning, and the ruleset must not be re-enabled before the approver is fixed |
| `protection_unknown` | the rules API could not be read; warning, never a failure |

The preflight is reporting only: it never sets an error, so a red auto-approve
run still means a real regression. Its guard is
`development/scripts/test_auto_approve_merge_protection_preflight.py`.

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
