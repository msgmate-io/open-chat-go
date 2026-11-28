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

### Forgejo CI backend

The CI deployment (`ci.msgmate.io`) runs from the Forgejo instance at
`git.msgmate.io`. The `.forgejo/workflows` copies are the authoritative CI
definitions there. The GitHub mirror carries the application code and releases but no longer runs a duplicate GitHub Actions CI surface.
The git integration is configured with a `forgejo` bootstrap token
(`provider: forgejo`, `base_url: https://git.msgmate.io`) so repository
indexing, issue creation and PR sessions operate against the same backend.

A read-only mirror of every repository is published at
`git-mirror.msgmate.io`; it is populated by source-side push mirrors and has
Actions disabled. The legacy `open-chat-go-v0` tree is intentionally excluded.

### The promotion hop is agent-closable, and verifying it is the price

Merging the `[Dev promotion]` pull request is what deploys ci.msgmate.io, so
while that merge stayed a maintainer click the loop could never close itself:
tickets piled up as "implemented" but never "live" (ci#1055). The development
lead may therefore merge the promotion PR on its own once **all required checks
on it are green** - and must then verify that the deployment actually serves the
promoted code. The two decisions are implemented once, as unit-tested
predicates, in `development/ci/scripts/ci-flows/promotion_hop.py`:

```bash
development/ci/scripts/ci-flows/promotion_hop.sh merge                 # may the hop be merged now?
development/ci/scripts/ci-flows/promotion_hop.sh deploy <VERSION>      # is that version live?
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

Because both states are wrong for it, the `dev` -> `main` promotion step in
`run-pr-ci-tests-and-deployments.yaml` **never arms the hop** with
`gh pr merge --auto` (ci#1066). It refreshes the PR, then reports
"awaiting a maintainer merge" and stops: an armed merge either can never land
(ruleset `active`, no approver) or lands before the required check has run
(ruleset masked), which turns a green promotion PR into a false proof that the
approval path works. Merging is owned by `auto-approve-consolidation.yaml`, the
only place that knows the approver identity - and, while the hop is human-merged,
a red promotion is *not* the signal for a broken CI; a red auto-approve run is.

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
`development/ci/scripts/ci-flows/test_auto_approve_merge_protection_preflight.py`.

The *decision* that preflight feeds - "is the hop unattended for the right
reason, and is re-enabling the ruleset the correct next step?" - is a pure
predicate in `development/ci/scripts/ci-flows/promotion_hop_readiness.py`, so nobody has to
re-derive it from raw API payloads (which is how the masked state passed for a
healthy hop in the first place):

```bash
# live, read-only (needs a token with `rules:read`; never prints the credential)
python3 development/ci/scripts/ci-flows/promotion_hop_readiness.py --live --repo msgmate-io/open-chat-go
# or from a saved observation document
python3 development/ci/scripts/ci-flows/promotion_hop_readiness.py observation.json --json
```

| Verdict | Exit | Meaning |
| --- | --- | --- |
| `ready_approver` | 0 | ruleset `active`, a write-access approver identity is usable: the hop can merge unattended |
| `ready_bypass` | 0 | ruleset `active`, a bypass actor covers the automation identity: the hop can merge unattended |
| `needs_approver` | 1 | ruleset `active`, no usable approver and no bypass actor: the hop is human-gated (the ci#1066 defect, not a regression) |
| `masked` | 2 | the review requirement is not enforced; `safe_to_enable` says whether the approver path is already in place |
| `unknown` | 3 | the observation is incomplete or the API could not be read - never reported as `masked` |

The effective-rules answer (`rules/branches/main`) is authoritative for *whether*
the requirement is enforced; the ruleset resource is authoritative for its
enforcement level and bypass actors. The tool joins the two, so a ruleset that
claims `active` while `rules/branches/main` answers `[]` is reported as `masked`.
Its guard is `development/ci/scripts/ci-flows/test_promotion_hop_readiness.py`.

Every PR needs a line of its own, `Closes msgmate-io/open-chat-go-ci#<n>`, in
its body (`.forgejo/PULL_REQUEST_TEMPLATE.md` already carries a `## Tracker`
section for it); `PR Test Merge Requirement` reads that and nothing else.
GitHub cannot auto-close a cross-repository issue, so the line is both the gate
input and the closure instruction. Agent-facing working notes - including the
full set of PR conventions (base, draft state, labels, tracker line, truthful
test reporting) - live in [`AGENTS.md`](AGENTS.md).

### Attributing a relayed private-sandbox failure (ci#1174)

A `Private sandbox lw` run that fails *every* confirmable/interaction job while
reporting **no failed test names** is a stalled interaction, not a test failure:
the job hits `interaction timed out` with `state=active` and no bot turn
finishing. Classify it from the tested commit, not from the job text:

| Tested commit contains | Relay line to expect in the backend log | Meaning |
| --- | --- | --- |
| no `backend/suspend/in_turn_park.go` (pre-ci#1178) | none | the ci#1116 in-turn park waits for a decision that only exists after the generation returns, so every confirmable round burns the suspension TTL - the run is stale, re-run on a commit that carries the gate |
| the gate, `OCI_CONFIRMABLE_IN_TURN_PARK` unset/`false` | `In-turn confirmation park disabled (OCI_CONFIRMABLE_IN_TURN_PARK): ending the turn on <n> pending confirmable call(s) of chat <uuid>` | the turn took the pre-ci#1116 terminal pause; the approval starts an ordinary follow-up turn |
| the gate, lever set to `1`/`true` | `Confirmable tool call <id> in chat <uuid> was not decided within <ttl>; dropping the park ...` on expiry | a live park is waiting on the TTL - set the lever back to unset/`false` unless the decider can deliver while the generation is parked |

Both lines are emitted at `info` (they fire at most once per confirmable round),
so they are visible at the deployment log level. `parkAndAwaitConfirmationRound`
renders the first one through `inTurnParkDisabledLine`, pinned by
`backend/api/msgmate/confirmable_round_test.go`.

Do not let the log text alone decide the class: a probe log also echoes the
`run:` block it executed, so the harness source lines that *print* annotations
(`echo "::error::..."`, `printf '::error::...'`, `warn "::warning::..."`) appear
in the log next to the real markers. `sandbox_turn_stall_class` in
`development/scripts/sandbox_provider_failure.sh` therefore strips ANSI colour,
ignores annotation markers quoted inside `echo`/`printf`/`warn`, and ignores the
derived `##[error]Process completed with exit code` / `##[error]sandbox test ...
failed` annotations before counting other failures; without that, a run whose
only real signal is a stall reports `stalls=<n>, other_failures=<m>` and never
classifies (ci#1171: eleven identical confirmable probes, `stalls=2`,
`other_failures=9`, all nine echoed source text). Regression cases for both the
plain and the ANSI-coloured shape live in
`development/scripts/tests/test_sandbox_provider_failure.sh`.

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

## Confirmable confirmation stalls (ci#1172 / ci#1177 / ci#1178)

Symptom: a long coder test (or an interactive chat) is left `state=active` with
`last_message=false` until the probe harness reports
`interaction timed out after 720s (runtime=native, state=active, messages=N,
last_message=false|...)` - no assertion failure, no bot turn. This is the shape
of the ci#1172 relay: eleven `Private Test msgmate_coder_*` jobs, one identical
timeout each, no failed test name anywhere in the log.

Reading the backend log, in order:

1. `bot-reply generation finished: chat=<uuid> ok=true task=<id>
   continuation=<bool> elapsed=<dur> err=<nil>` - turn 1 completed. If `elapsed`
   is close to the in-turn park budget (ci#1177: about **20s** by default, set
   with `OCI_CONFIRMABLE_PARK_WAIT` - the single lever since the ci#1175/ci#1177
   consolidation; the ci#1175 spelling `OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET` is
   still honoured as an alias - and never more than half the suspension
   TTL), the generation burned an in-turn confirmation park instead of doing
   provider work. On a build that predates ci#1177 the same comparison reads
   against `suspend.DefaultTTL` (10m), because the park then awaited the whole
   durable suspension instead of its own budget. The matching
   `bot-reply generation start: chat=<uuid> budget=<dur> ...` line should be
   present for every finished line (ci#1161/ci#1162 bracketing pair).
2. `confirmable action <id> in chat <uuid>: continuation=continuation_queued
   (event <name>)` (not `continuation=resumed_turn`) proves the park was already
   gone when the approval landed - the pending `confirmable_actions` row is only
   persisted *after* the generation returns, so a decider that reads durable
   state can never satisfy a live in-turn park.
3. `Confirmable tool call <id> in chat <uuid> was not decided within <budget>;
   dropping the park and keeping the pause behaviour` is the explicit expiry
   line (Info level since ci#1178; it used to be silent at Debug). Since ci#1177
   the quoted duration is the park budget (~20s), not the suspension TTL; on an
   older build it reads `10m0s` and matches `suspend.DefaultTTL`.

The harness log carries the same fingerprint one layer up: an
`[interaction] pending confirmable action approved` line that lands ~10m after
`[sandbox-test] running interaction for ...` (the failing ci#1172 job approved
at +10m08s). That lag is the park TTL, not harness slowness - the approval can
only be read once the row exists. With the in-turn park enabled, every
confirmable call of the round parks (ci#1147), so on the pre-ci#1177 code a
two-call round burns 2 x 10m against a 720s probe timeout and the job cannot
pass; with the ci#1177 budget the same round spends ~2 x 20s and the park is
gone long before the probe gives up, so a park stuck at the full TTL family
means the bound is not on the tip under test. That arithmetic, not a slow
provider, is what a relay of this family means.

Also check which code was actually under test before concluding "main is fixed":
a private-sandbox run's title and `headBranch` say `main`, but the per-job
`TARGET_REF` / `SOURCE_SHA` / `BACKEND_IMAGE_INPUT` env in the "Run private
sandbox test" step name the commit that was built. The ci#1172 run tested
`82a10647` (pre-gate); the green re-run `44926643459` tested `SOURCE_SHA
09f7e01f` - the `dev` commit that carries the gate - even though its title says
`main`. Gate presence on the tip itself is `git cat-file -e <sha>:backend/
suspend/in_turn_park.go`.

The in-turn park is bounded by its own budget since ci#1177 (about 20s,
`OCI_CONFIRMABLE_PARK_WAIT`, capped at half the suspension TTL; the ci#1175
`OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET` is a documented alias of the same lever,
with the canonical key winning when both are set) and additionally
gated by `OCI_CONFIRMABLE_IN_TURN_PARK` (default **off**, see
`backend/suspend/in_turn_park.go`, ci#1178). Leave the gate off: with the gate
off a confirmable round ends the turn as before ci#1116, the handler persists
the pending action, the approval misses the (absent) park and queues an
ordinary ~2s continuation turn. Enable it only where a decision can be
delivered while the generation is still parked - and only on a tip that carries
the ci#1177 bound, otherwise the budget is the whole 10m TTL again.

Diagnostics checklist for a recurrence:

- `grep "was not decided within"` in the backend log: any hit means a park
  waited out its budget, i.e. the flag is on somewhere it should not be. Check
  the duration it prints: `10m0s` means the tip predates ci#1177's bound.
- `grep "generation finished"` for the continuation turn: a `generation start
  ... continuation=true` with no matching `generation finished` means the
  continuation itself is wedged - re-open with that run id rather than
  re-triaging the park.
- `grep -c "can't cd to"`: the workspace-root family (ci#1166) always logs at
  least one such line. **Zero** hits *plus* the stall fingerprint above means the
  run belongs to this section, not to workspace-root, whatever the relay ticket
  claims: the ci#1171 relay (run `44837783519`, checkout `a6ad3ea5`) was filed as
  workspace-root, yet its log carries 0 `can't cd to` lines and 20
  `interaction timed out after 720s (... state=active ... last_message=false)`
  lines across ten of the eleven failing jobs, and the tested commit predates the
  gate (`git cat-file -e a6ad3ea5:backend/suspend/in_turn_park.go` fails) and is
  not even an ancestor of `origin/main`. Such a relay is stale, not a new defect:
  close it against a green re-run on a gated tip and put new evidence on ci#1166.
- One job of that run is *not* a stall: `msgmate_coder_command_confirmation` got a
  clean `READY` reply (`interaction state: finished`) in 617s and then died in the
  verifier with `msgmate_coder_interactions.sh: line 133: echo: write error:
  Broken pipe`, so `sandbox_turn_stall_class` correctly stays empty for it. That is
  a SIGPIPE/`pipefail` bug in the harness helper, which lives in
  `open-chat-go-ci` - do not chase it here as a model regression.
