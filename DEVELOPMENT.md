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

The `dev` branch is the **integration trunk** (ci#833). Labels follow the hop
they belong to:

| PR shape | Base | Labels | Deploys? |
| --- | --- | --- | --- |
| feature / bugfix / docs / chore | `dev` | `test` only | no |
| consolidation (`chore/consolidate-*`) | `dev` | `test`, `e2e` only | no |
| promotion (`dev` → `main`, titled `[Dev promotion]`) | `main` | `test`, `e2e`, `deploy`, `sandbox-test` | yes → ci.msgmate.io |
| production promotion (`main` → `production`) | `production` | keeps the production labels | yes → msgmate.io |

Policy:

- Use `deploy` and `sandbox-test` **only on the release hops**: the
  `dev` → `main` promotion PR and the `main` → `production` promotion PR.
  Feature, bugfix, docs and chore PRs carry the ordinary code-change labels
  only; a consolidation PR targeting `dev` carries `test,e2e` and never a
  deployment label. `Auto-approve consolidation PRs` refuses to merge a
  `dev`-based consolidation PR that carries `deploy`/`sandbox-test`.
- The `dev` → `main` promotion PR is created and refreshed **automatically** by
  `Run PR CI Tests and Deployments` (job `open-dev-to-main-promotion-pr`) on
  every merge to `dev`. It is refreshed in place — never closed and recreated,
  which would cancel its labelled CI run — and its label set is applied by the
  automation. Do not toggle the labels by hand.
- Deployment is decided by the merge *base ref* (job
  `resolve-merged-deployment`), and an unknown base ref is a hard error:
  `dev` dispatches **no** deployment, `main` deploys the CI environment
  (ci.msgmate.io), `production` deploys msgmate.io.
- A PR *based on* `dev` never dispatches a pre-merge deploy either: the
  context step clears `should_deploy` for `base.ref == 'dev'`, so a stray
  `deploy` label on a dev-trunk PR cannot open a staging PR environment. Only the
  `dev` → `main` promotion PR (base `main`) deploys. A stray `sandbox-test`
  label on a dev-base PR is therefore not inert but **fail-closed**: with no
  deploy dispatched, `run-pr-ci-sandbox-tests-after-deploy` is `skipped` (its
  `if:` only tolerates a skipped dispatch on `production`), and the merge gate
  then rejects the PR with *PR CI sandbox tests did not run for this commit* —
  the PR stays unmergeable until the label is removed.
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

#### The dev integration trunk

Feature work lands on `dev` (protected by the `Dev (Integration) Merge
Protection` ruleset, whose only requirement is the green `Run PR CI Tests and
Deployments` check). `main` is then promoted from `dev` through the automatic
`[Dev promotion]` PR, so `main` only ever receives a batch that already passed
PR CI on `dev` plus the `deploy`/`sandbox-test` verification of the promotion
run. The chain is:

```text
feature/* --(test)--> dev --(test,e2e, consolidation)--> dev
                     |
                     auto-refreshed [Dev promotion] PR (test,e2e,deploy,sandbox-test)
                     v
                    main --(main -> production promotion)--> production
```

### One leaf issue per pull request

Every feature or bugfix pull request implements exactly one **leaf** issue in
`msgmate-io/open-chat-go-ci`, and its body carries a standalone closing line:

```text
Closes msgmate-io/open-chat-go-ci#123
```

The `PR Test Merge Requirement` rejects a feature/bugfix PR without exactly one
open, non-umbrella linked issue. A large issue is split into child leaf issues
before the work starts; phase PRs for the same issue are not used. Umbrella and
tracker issues carry the `umbrella` label and are closed manually once every
child is done.

GitHub cannot auto-close an issue in another repository, so the consolidation
runner closes every linked leaf issue after the batch merges (a direct merge
closes them in the same step). The only exemptions are production promotion PRs
(base `production`), the `dev` -> `main` promotion, consolidation PRs
(`chore/consolidate*`, exempt from the whole linked-issue requirement since
ci#862: they aggregate already-merged leaf work and may carry no closing line at
all), `no-issue` maintenance PRs and `dependencies` updates.

Provider-side sandbox failures are classified, not guessed: the sandbox benchmark
workflow sources `development/scripts/sandbox_provider_failure.sh` to tell a
provider credential/balance error (HTTP 401/402, never retried) and a provider
that cannot be reached (HTTP 408/429/5xx or a connection-level error, transient
provider infrastructure, matched case-insensitively like the Go comparison) apart
from a real coder-sandbox regression, so the
summary names the cause once instead of leaving one identical failure row per
test. An in-flight-credit 402 (`would exceed your available credits given your
current in-flight requests`) is classified as transient, not as a drained
balance: the key does have balance and the rejection clears as soon as the
concurrent requests drain, so it keeps its retry instead of being frozen as an
account failure (ci#769 — an uncapped matrix tripped it for every test at once,
so no test ever got its retry and the whole guard issue went red). The classifier
is pinned by
`development/scripts/tests/test_sandbox_provider_failure.sh`, which runs in the
Pin Freshness Gate.

The `PR Test Merge Requirement` gate is **label-driven and fail-closed** for every
requested suite: when a suite's job reports `skipped` while the label that
requests it (`test`, `ai-test-mock`, `e2e`, `sandbox-test`, …) is live on the pull
request — or, for the Go tests, while the job's own `should_test` output says it
was requested — the gate errors out instead of accepting the missing run. A suite
that was *not* requested stays a pass. Cancelled runs are only accepted when a
replacement run for the same commit adopted their verdict. The
`run-pr-ci-ai-mock-tests` case in that gate is pinned by
`development/scripts/test_pr_ci_ai_mock_gate.py` (stdlib-only, runs in the Pin
Freshness Gate) so the label guard cannot silently regress to `skipped) ;;`.

The readiness gate's other half — the `ci#722` redis probe budget — is pinned the
same way: `development/scripts/redis_readiness_contract.py` re-derives the
`probe_timeout_too_low` / `probe_budget_too_short` / `startup_probe_missing` /
`redis_cpu_request_missing` findings from a redis Deployment manifest, emits the
exact strategic-merge patch the gate proposes (checked in as
`development/redis-readiness/ci-722-redis-probes.patch.json`) and exits non-zero
while any check fails, so a redis pod without a probe budget or CPU request cannot
silently come back and take the queue/state endpoint (and every freshly dispatched
interaction) down with it. The gate itself stays read-only; applying the patch is
the operator's call. Its regression test,
`development/scripts/test_redis_readiness_contract.py`, runs in the Pin Freshness
Gate too.

That patch only fixes the *live* object, though, and the Deployment is re-rendered
on every deploy from the private Helm chart (`msgmate-io/open-chat-go-helm-chart`,
`backend/templates/redis.yaml`), which still renders the kubelet probe defaults
and no CPU request — so the gate finding comes straight back after the next
`helm upgrade`. The chart half of the fix is carried in this repository as
`development/redis-readiness/ci-722-chart.patch` plus the rendered before/after
fixtures next to it (`chart-redis-template.{before,after}.yaml`, captured from the
chart template and the chart's own redis values), and
`development/scripts/redis_chart_readiness_contract.py` renders that Go-template
subset and checks the result against the same contract as the live checker — so
"the chart renders a redis container the readiness gate would accept" is checkable
before any deploy:

```bash
development/scripts/redis_chart_readiness_contract.py \
  --chart-file development/redis-readiness/chart-redis-template.after.yaml
development/scripts/redis_chart_readiness_contract.py --chart-dir /path/to/open-chat-go-helm-chart
```

The automation identity has no write access to the chart repository, so an
operator applies the patch at the chart root
(`git apply development/redis-readiness/ci-722-chart.patch`) and then the same
`kubectl patch` shown by the gate; until the chart is updated, a redeploy
re-introduces the finding. The chart checker and its regression test
(`development/scripts/test_redis_chart_readiness_contract.py`, stdlib-only like
its sibling) run in the Pin Freshness Gate.
The compose profiles are the second consumer of that same redis, so the `ci#722`
numbers are mirrored there as well: `docker-compose.yaml`, `docker-compose.ci.yaml`
and `docker-compose.pro.yaml` carry a healthcheck of `interval: 5s`,
`timeout: 2s`, `retries: 12` and `start_period: 5s` — a budget that tolerates a
minute of sustained failure — plus a `deploy.resources` reservation of `0.1` CPU
with a `0.5` CPU limit. A healthcheck that gives up after a few seconds of
contention marks the container unhealthy and a redis without a CPU reservation is
starved behind the agent workloads on a shared runner; either way the queue/state
endpoint disappears under load and freshly dispatched interactions are dead on
arrival. `development/scripts/redis_compose_readiness_contract.py` is the compose
half of the contract: stdlib-only (the Pin Freshness Gate has no PyYAML), it
extracts the `redis` service with a small block-mapping parser, re-derives the
same finding ids (`probe_missing`, `probe_timeout_too_low`,
`probe_budget_too_short`, `startup_probe_missing`, `redis_cpu_request_missing`,
…), prints GitHub `::error::` annotations and exits non-zero while any check
fails, so a flapping redis healthcheck cannot silently come back. Its regression
test, `development/scripts/test_redis_compose_readiness_contract.py`, runs in the
Pin Freshness Gate next to the Kubernetes half.

The PR-CI progress comments follow the same rule. Every job that upserts the
`<!-- open-chat-pr-ci-progress -->` comment must address an explicit, non-empty
`repository`/`PR number` pair: pass it through step `env` with the event context
as fallback (`github.repository` / `github.event.pull_request.number`) and let
`development/scripts/upsert_pr_ci_progress_comment.sh` own the marker lookup plus
the create-or-update call. An empty target must log a `::warning::` and skip with
exit 0 before the first HTTP call — never interpolate a raw
`needs.*.outputs.*` expression into an `api.github.com` URL, which collapses to
`repos//issues//comments` and fails the step with `curl: (22) ... 404` (ci#745).
`development/scripts/test_pr_ci_progress_comment_target.py` and the hermetic
`development/scripts/tests/test_upsert_pr_ci_progress_comment.sh` pin both halves
in the Pin Freshness Gate.

#### Autonomous approval and merge of green consolidation PRs

A consolidation PR is always authored by the automation identity, which cannot
approve its own PR, so `.github/workflows/auto-approve-consolidation.yaml`
approves **and merges** it once it is provably green:

- It triggers on completion of the required **Run PR CI Tests and Deployments**
  workflow, and accepts exactly two PR shapes in this repository (open, not a
  draft, no conflicts): `chore/consolidate-*` → `dev` or `main`, and
  `dev` → `main` (the promotion PR). A `closes`-linked automation identity still
  cannot approve its own PR, which is why this workflow exists.
- Before merging it re-reads the PR from the API and refuses to merge unless the
  required CI check concluded `success` and every other check on the PR finished
  green — a red or pending check stops the merge even though the workflow is
  allowed to bypass the ruleset.
- It first posts the one required approving review from a *distinct* reviewer
  identity, then merges normally (the `main` ruleset's own gates apply). If no
  usable reviewer identity is configured it retries the merge with `--admin`,
  i.e. through a ruleset bypass, with the API-verified green state as the gate.
- A PR that is merely `BEHIND` gets its branch updated instead (which re-runs CI
  and this workflow); an already merged PR counts as success, so re-runs and
  races are harmless.
- Resolving *which* PR the finished run belongs to goes through
  `GET /repos/{repo}/commits/{sha}/pulls`, which this repository has seen answer
  intermittently with an empty/truncated body (`unexpected end of JSON input`),
  so the lookup is retried up to three times with a 5s/10s backoff before it
  falls back to a `gh pr list` matched on `headRefOid`. The retry only accepts a
  *usable* payload: `gh api --paginate` emits one JSON document per page, so the
  guard slurps the documents and flattens them (`jq -ces '... then add ...'`),
  which keeps single-page answers unchanged, turns a multi-page `[...][...]`
  answer — not one valid JSON document — into a single array, and rejects an
  empty body, `null` or an error object so the retry (and eventually the
  fallback) still runs instead of the job dying red on a flaky API. Both paths
  exhausted is only fatal when the fallback body is unusable; a valid empty
  listing is a no-op. Pinned by
  `development/scripts/test_auto_approve_flaky_pulls.py` and
  `development/scripts/test_auto_approve_stale_check_instances.py` (ci#847: the
  newest-per-context normalization of the rollup, so a superseded CANCELLED run
  cannot veto its green replacement) in the Pin Freshness
  Gate job.

The preferred configuration is a dedicated reviewer identity (GitHub App or a
fine-grained token with `pull_requests: write`) stored as the repository secret
`CONSOLIDATE_APPROVER_TOKEN`; alternatively add a ruleset bypass actor for the
consolidation automation. With neither, the run warns and leaves the human
approval in place. All of this is only wired for consolidation PRs and the
`dev` → `main` promotion PR — feature, bugfix, chore and docs PRs still need a
human approval and merge.

The promotion hop is the one that deploys: merging the `dev` → `main` PR
triggers `resolve-merged-deployment` with `merge_target=main`, which dispatches
the ci.msgmate.io deployment (`msgmate-io-ci`) and opens the `main` →
`production` promotion PR in turn.

##### A deployment dispatch cannot claim the CI host (ci#890)

`ci.msgmate.io` was observed serving a VERSION that existed only on unmerged
branches — `main` sat one release behind the host — while the run history showed
a *PR-preview* deployment (`msgmate-io-pr-deployment`) for an already **closed**
pull request running *after* the `main`-merge deploys. A preview deploys with the
PR-scoped overrides it was dispatched with, so a preview carrying no (or a stale)
override falls back to the deployment-wide identity of
`values.msgmate-io-pr-deployment.yaml` and re-asserts whatever identity the CI
deployment already had.

`development/scripts/pr_ci_pr_deploy_target_guard.py` is the last gate before the
dispatch: a pure predicate over the very outputs the payload is built from
(`deployment_name`, `NAMESPACE_OVERRIDE`, `DEPLOY_HOST_OVERRIDE`,
`HELM_RELEASE_OVERRIDE`, `PR_NUMBER`, plus the live `PR_STATE` the step resolves
from the API) that refuses, fail-closed, (a) a `msgmate-io-pr-deployment`
dispatch without PR-scoped overrides (a blank override is refused *because* the
CI-side deploy only honours a non-empty one), (b) any dispatch naming the CI
namespace `msgmate-io-ci` or the host `ci.msgmate.io`, (c) a preview for a
closed/merged PR and (d) a non-preview deployment carrying PR-scoped overrides
(so a preview host/namespace cannot leak into a merged deployment). Failing here
means the deploy is never dispatched, so the host it would have claimed is
untouched. Its regression test,
`development/scripts/test_pr_ci_pr_deploy_target_guard.py` (stdlib-only, like its
siblings), runs in the Pin Freshness Gate.

#### A cancelled (superseded) CI run is not a failed gate (ci#659)

The workflow's concurrency group is keyed by PR number *and* head SHA with
`cancel-in-progress: true`, and a later job cancels superseded runs of the same
branch. A burst of `labeled`/`unlabeled` events therefore cancels the in-flight
run for the *identical* commit, while the `always()`-gated
`PR Test Merge Requirement` job still executes inside that cancelled run with
every `needs.*.result == 'cancelled'` - which reported the required check as
failed for a commit whose tests never ran (run 39245536652 on #835).

Blanket-passing the cancelled case would make an untested tree look mergeable, so
the cancellation is reconciled instead (`development/scripts/pr_ci_superseded_reconcile.py`,
unit-tested by `development/scripts/test_pr_ci_superseded_reconcile.py` in the
Pin Freshness Gate job): a cancelled gate is accepted only when the commit no
longer needs gating (PR closed, or a newer commit is the live PR head) or when a
*replacement* run for the exact same commit reached the same gate job with a real
verdict, whose conclusion is adopted. No replacement, a replacement still queued
after `PR_CI_SUPERSEDED_WAIT_SECONDS` (default 3600s, repo variable
`PR_CI_SUPERSEDED_WAIT_SECONDS`), an unknown/cancelled gate job or an API error
all fail closed, so the gate never turns green on an untested commit.

The wait budget must outlast a *whole* replacement run, not just the gap to its
gate job: run 39370413734 (PR #879, commit `886d6e26`) took 2550s from creation
to its gate verdict, so the original 1500s default expired ~12 minutes early and
run 39370411496 reported the required check as failed (`replacement run
39370413734 still pending after 1500s`) for a commit whose replacement gate job
then concluded `success` — the same red-on-a-cancelled-run symptom ci#659 set out
to remove, only reachable through the budget instead of the reconciliation
logic. The default is now one hour, pinned to the workflow default by
`WorkflowWiringTests`.

A longer default is still a threshold, so the expiry path is made
self-identifying rather than silently shifting the cliff: exit code 3 (budget
expired before any replacement could testify) prints a `budget-expired:` summary
line *and* a `::warning::` annotation naming the budget. That keeps a latent-red
gate from a budget expiry distinguishable in the logs from a replacement run that
really concluded non-green (exit 1) — the two look identical on the check
rollup, and the warning tells the maintainer to raise
`PR_CI_SUPERSEDED_WAIT_SECONDS` instead of debugging the reconciliation.

Because a run of this workflow is close to the 100-item jobs page, the run and
job listings are read page by page (`api_pages`): a single-page read could miss
the gate job, mistake the run for one that never gated the commit and adopt the
verdict of an older run for the same SHA.

Waiting is not always the right answer, because the waiter is also the blocker:
the concurrency group is held by *this* run until it completes, and the
`always()`-gated job keeps a cancelled run alive for the whole wait budget, so a
replacement run for the identical commit can be starved by the very run that is
waiting for it (observed on #892: run 39407028825 stayed `pending` for 12+
minutes with an empty repository queue, blocked behind the cancelled run
39407027496). A replacement that has been unable to start for
`PR_CI_SUPERSEDED_YIELD_SECONDS` (default 60s, repo variable of the same name)
therefore makes the reconciler cancel its own run - `yield_group`, exit code 5,
`accepted=false` - and hand the group over; a replacement that is already
`in_progress` is left to finish, and a `409` on the cancel (already cancelling)
still counts as yielded. The replacement run's gate owns the verdict from then
on. Set the variable to `0` to disable yielding and keep the old
wait-until-timeout behaviour.

The reconciler runs from the workspace, so the `PR Test Merge Requirement` job
starts with a shallow `actions/checkout` of the pull request's head commit. The
step is `continue-on-error: true` (the gate step decides), which is why the
reconciler also checks for its own file and emits an `::error::` annotation with
`accepted=false` instead of dying quietly: the very first version of this fix
shipped without the checkout and therefore never engaged (run 39261844509 on
#844). `WorkflowWiringTests` pins the checkout-before-reconciler ordering.

The same payload-versus-live distinction applies to *every* job, not just the gate
(ci#659 follow-up). `gh pr create -l ...`, and the default `test` label that
`no_labels_guidance` attaches in a second API call, both land after the event was
emitted, so an `opened` payload is label-free. The entry job
(`run-pr-ci-tests-and-deployments`) used to gate *itself* on that payload and was
therefore skipped while the always()-gated `PR Test Merge Requirement` read the
live list and failed with `PR CI did not run for this commit with the test label`,
for a commit whose suites were never asked to run (run 39373933834 on #880, whose
sibling run 39373933693 had already been cancelled by concurrency). Every job now
starts from the live snapshot: the entry job runs on every pull-request event and
publishes `should_*` outputs derived from the API label list (including
`should_android_build_test`), and `run-pr-ci-go-tests`, `run-pr-ci-ai-mock-tests`,
`sandbox-android-image-ensure` and `run-pr-ci-android-build` gate on those outputs.
No job-level `if:` may read `github.event.pull_request.labels` (only the
informational `no_labels_guidance` job and the `pin-drift-allowed` escape hatch
still inspect the payload); `LiveLabelGatingTests` in
`development/scripts/test_pr_ci_superseded_reconcile.py` pins that.

#### Startup bootstrap survives an overlapping pod's SQLite write lock (ci#679)

The SQLite database lives on a single ReadWriteOnce volume, so two backend
processes can briefly share one database file during a rollout (a *restarted*
old pod can still be terminating while the new one boots). SQLite grants exactly
one write lock for the whole file, and the outgoing process may hold it for
longer than the connection's `busy_timeout(10000)`, so a startup write could
fail with

```
add-bot-from-config[0]: database is locked (5) (SQLITE_BUSY)
```

and the process exited (1) - which wiped every in-flight agent turn. The
condition is transient by construction and the startup pass is an idempotent
upsert, so it must never be fatal:

- `database.RetryOnBusy` (`backend/database/sqlite_busy.go`) retries an
  operation while it fails with a transient lock error (`SQLITE_BUSY` /
  `SQLITE_LOCKED`, recognised through the driver's result code or the
  "database is locked" message) using exponential backoff, and returns anything
  else immediately. The default budget (20 attempts, 200ms -> 5s) tolerates well
  over two minutes of foreign write lock - an overlapping pod only holds it while
  it terminates. The connection keeps `busy_timeout(10000)` +
  `journal_mode(WAL)` (`backend/database/db_sqlite_driver_default.go`), which the
  new pragma test pins.
- Every startup write in `runServer` goes through `runStartupBootstrapDBStep`
  (`backend/cmd/bootstrap_busy_retry.go`): `seed-model-configs`, the
  root/default/extra users, `bootstrap.users`, `add-bot-from-config`, the
  integration bot configs, and the SSH, sandbox, opencode, git, MCP, kubernetes,
  integration-hook and default-model-access passes. Only a real error - or an
  exhausted retry budget, which now names the lock and the action to take - still
  aborts startup.
- `util.CreateUser` / `util.CreateUserPwPreHashed` no longer call `log.Fatal` on
  a write error: they return the error (wrapped, so it unwraps to the driver's
  lock error) for the retry loop to classify. The `log.Fatal` inside the writer
  bypassed any retry - that is exactly how the crash-loop happened.
- `backend/cmd/bootstrap_busy_retry_test.go` and
  `backend/database/sqlite_busy_test.go` reproduce it: a second process takes
  `BEGIN IMMEDIATE` on the same file, the booting process starts writing, and the
  step has to retry and commit exactly one row; a non-lock error still surfaces.
- The busy retry alone does not stop both pods from running the *same* seed at the
  same time, which duplicates work and can still collide on unique keys.
  `database.RunBootstrapSeedOnce` (`backend/database/bootstrap_seed.go`) adds the
  missing half: exactly one process applies a given seed, the others follow it and
  then continue. A `bootstrap_seeds` row is claimed with an atomic
  `INSERT ... ON CONFLICT DO NOTHING`, so only one writer wins even across
  processes sharing one file; a fingerprint of the seed payload is stored with the
  claim, so an unchanged payload is applied once and never again while a changed
  payload (a rollout with new bootstrap config) is re-applied. Claims of a pod that
  died mid-seed expire after `BootstrapSeedClaimTTL` (5m) and are stolen, so a
  crashed pod cannot wedge the bootstrap. Every access - including the guard table
  migration and the completion marker - goes through `RetryOnBusy`. The guard
  table is created with a plain `CREATE TABLE IF NOT EXISTS` instead of
  `AutoMigrate`: concurrent `AutoMigrate` calls lose their check-then-create race
  ("table `bootstrap_seeds` already exists"), which is exactly the overlap the
  guard has to survive. `bootstrap.users` and `add-bot-from-config` in `runServer`
  run through `runStartupBootstrapSeedOnce` on top of the retry loop;
  `backend/cmd/bootstrap_seed_test.go` pins the single-writer behaviour (two pods,
  one row), the re-apply on changed payload, the lock-overlap survival and the
  fingerprint stability.
- Deployment strategy: the SQLite volume is ReadWriteOnce, so the deployment must
  not roll with overlapping pods (`strategy: Recreate` in the
  `msgmate-io/open-chat-go-helm-chart` deployment; the live staging Deployment
  was already patched to it). The retry above is the safety net for a restart and
  for multi-process setups (workers, ci#648), not a licence to roll concurrently.
  The chart change itself is tracked as
  [open-chat-go-helm-chart#3](https://github.com/msgmate-io/open-chat-go-helm-chart/issues/3)
  (that repository grants this bot identity no write access and has forking
  disabled, so the verified patch lives in that issue for a maintainer to land).
- Still a maintainer/ops item: watch the 1Gi SQLite PVC (it grows with
  interactions and history) and raise it or add retention when it approaches the
  limit.

#### Test helpers close their SQLite pool before `t.TempDir` removes the directory (ci#920)

A test helper that points SQLite at `filepath.Join(t.TempDir(), ...)` and never
closes the `gorm` pool leaves the pool alive past the test. Because the database
runs in WAL mode (`sqliteDSNPragmas`,
`backend/database/db_sqlite_driver_default.go`), a pooled connection can recreate
the `-wal`/`-shm` sidecars while `t.TempDir`'s `RemoveAll` walks the directory,
racing into

```
TempDir RemoveAll cleanup: unlinkat /tmp/Test.../001: directory not empty
```

and, once the database file is unlinked underneath a still-open connection, into
`disk I/O error (522)`.

- `database.SetupTestDatabase(t, cfg)` / `database.CloseTestDB(t, DB)`
  (`backend/database/testdb.go`) are the shared helpers. `t.TempDir` registers its
  `RemoveAll` cleanup *before* the helper registers the pool close and cleanups
  run LIFO, so the close always precedes `RemoveAll`. Use
  `SetupTestDatabase` instead of `SetupDatabase` in tests; a site that opens the
  pool itself (`gorm.Open`, `openSQLite`) registers `CloseTestDB(t, DB)` right
  after the successful open.
- `CloseTestDB` is nil-safe and tolerates a site that closes its own pool, since
  `database/sql` `Close` is idempotent - so it never double-closes.
- The ordering rationale lives in exactly one place instead of a per-package
  `closeXTestDB` copy; `backend/database/testdb_test.go` pins the close-before-
  `RemoveAll` ordering and the idempotent re-close.
- The audit is held in place by `TestTempDirSQLiteHelpersCloseTheirPool` in the
  same file: it scans the backend module's `*_test.go` files and fails when a
  file binds `t.TempDir()` to a SQLite pool (`SetupDatabase(`, `gorm.Open(`,
  `openSQLite(`) without referencing `SetupTestDatabase(`/`CloseTestDB(`, a
  package-local `close*TestDB(` helper (the shape the #919 `api/chats` fix ships,
  verified by applying that patch), or both `.DB()` and `.Close()` when it
  unwraps the pool itself. Requiring both halves there matters: a file that
  closes some unrelated handle and never touches the pool is exactly the shape
  `api/chats/browser_token_access_test.go` had. A new helper copied from a stale
  one fails there instead of flaking CI intermittently, which is the only failure
  mode this bug has.
- The scan skips the vendored/private checkouts (dot-dirs, `clients/`, `ci/`, plus
  `node_modules`/`vendor`/`frontend`/`dist`/`build`) for the same reason
  `backend/docguard` does: `openchat-integrations sync` and
  `openchat-integrations setup` materialize another repository's tree under
  `clients/integrations/` and `development/ci/`, and their tests cannot be fixed
  from here. Without that skip the guard went red on any tree that had synced
  (the integration `bootstrap_test.go` files bind `t.TempDir()` to a pool they
  never close), which is a false positive on someone else's code, not a
  regression in ours.
- `knownTempDirPoolViolations` in that file is the only escape hatch: it lists
  files whose fix is owned by an in-flight PR, and the guard fails on an entry
  that no longer violates, so the exception has to be deleted with the fix rather
  than quietly outliving it. It is empty as of the r56 consolidation (the three
  `api/chats` entries were #919 and went away with that fix).
- Known limitation of that scan, deliberate and *not* closed here: the predicate
  is per *file*, so a file that calls `SetupTestDatabase(` for one helper and a
  bare `SetupDatabase(` for another is exempted wholesale. The hole is latent
  today - the only three bare call sites on the tree are the `#919` ones and none
  of those files calls `SetupTestDatabase(` - but it is real. Closing it needs a
  per-function scan plus string-literal stripping (the guard's own fixtures embed
  `SetupDatabase(`/`gorm.Open(` literals, so naive occurrence counting would
  false-positive on the guard file itself). Worth a follow-up, not a bolt-on.
  Two further reasons the accepted `close*TestDB(` form is weaker than the
  "package-local helper" label suggests, both pointing the same way: it is a
  raw-text match, so a fixture or comment *spelling out* `closeChatsTestDB(`
  would exempt the guard file itself, and it fires on the helper's *declaration*
  alone - nothing checks that the helper is ever called, or called by every
  helper in the file. Same per-file hole, one more accepted token.

#### Detached DB work is drained before the teardown closes the pool (ci#811)

The helpers above close the pool before `t.TempDir`'s `RemoveAll`, but they can
only wait for work they can observe. A handler that answers its HTTP response and
then keeps writing through the `*gorm.DB` it was handed - a notification, an
auto-continuation follow-up - runs in a goroutine the pool-quiescence check cannot
see: it holds no connection at the moment it is checked, so `CloseTestDB` can
close the pool (and `RemoveAll` can walk the directory) between two of its
statements. The symptom is the same

```
TempDir RemoveAll cleanup: unlinkat /tmp/Test.../001: directory not empty
```

as ci#920, but no amount of extra waiting fixes it - only an explicit handshake
does.

- `database.DetachedWork` (`backend/database/detached_work.go`) is that handshake:
  `Go(fn)` runs `fn` in a tracked goroutine, `Wait(timeout)` blocks until the group
  is quiescent, and `Started`/`Pending` let a test observe that the tracked work
  was started and has finished (`backend/database/detached_work_test.go`).
  `RegisterDetachedWork(group)` publishes a package-level group to the shared test
  helpers, so `CloseTestDB` drains it first (`DrainRegisteredDetachedWork`, bounded
  by `detachedWorkDrainTimeout`, 10s).
- `backend/api/chats` keeps one such group, `chatsDetachedDBWork`
  (`backend/api/chats/detached_work.go`), and exposes `GoDetachedDBWork` for other
  packages that hand it a pool: `api/tools` uses it for its confirmation
  notification, `MessageSend.go` for the auto-continuation and finished-reply post
  that follow a send response, and `InteractionConfirmation.go` for the
  interaction-confirmed notification.
- A caller that only has the unwrapped `*sql.DB` can call
  `database.WaitForTestDBQuiescent(t, DB, timeout)` instead, which waits for the
  pool itself to go idle.
- The guard is `TestTempDirPackagesHaveNoUntrackedDBWriters` in
  `backend/database/testdb_test.go`. It scans the non-test files of every package
  whose tests bind a SQLite pool to `t.TempDir()` and fails when one starts a
  goroutine that mentions a DB handle without one of the accepted handshakes
  (`RegisterDetachedWork(`, `DrainRegisteredDetachedWork(`,
  `WaitForTestDBQuiescent(`). `knownAsyncDBWriterViolations` is the debt list of
  files drained by their own stop function (`session_cleanup.go` and
  `session_lifetime.go`, both `cancel` + `<-done`); an entry that stops matching
  fails the test, so the list cannot outlive its fix. The predicate itself is
  pinned against a synthetic leak/tracked tree by
  `TestScanForTempDirPackagesWithDetachedDBWritersClassifiesFixtures`, so the guard
  cannot silently stop recognising the shape it was written for.
- Known limitation, deliberate and *not* closed here: the scan is textual and per
  *file*, so it accepts a file that merely *mentions* a handshake token (even in a
  comment or a fixture) and it never checks that the accepted call is reachable
  from the goroutine it exempts. Closing that needs the same per-function scan plus
  string-literal stripping that the ci#920 note above already records as a
  follow-up, so it is folded into that item instead of duplicated here.

#### Expired sessions are swept, not just rejected (ci#729 follow-up)

Expired sessions were never removed: `resolveValidSessionFromRequest` in
`backend/server/middleware.go` rejects an expired token with 401/403, logout
deletes only the sessions it was asked to delete, and an automated turn deletes
its own session when it finishes. Everything else - the 24h browser sessions
`Login` writes, bot sessions whose turn died before their deferred cleanup ran -
stayed in the `sessions` table forever, on the one volume the note above flags as
a growth risk.

- `database.StartSessionSweeper` (`backend/database/session_cleanup.go`) sweeps
  from the server process: once at startup, then every interval until the stop
  function (deferred in `runServer`) is called - the stop is blocking, so no sweep
  can outlive the server.
- `database.DeleteExpiredSessions` deletes sessions whose `expiry` is older than
  `now - ExpiredSessionGrace` (1h). The grace is not needed for correctness (an
  expired token is rejected anyway) but keeps a row around long enough that clock
  skew or a refresh landing a moment late cannot race the delete.
- The delete is **unscoped** on purpose: `Session` embeds `gorm.Model`, so a plain
  `Delete` would only stamp `deleted_at` and leave the row's bytes behind - the
  exact growth this exists to stop. Soft-deleted sessions are invisible to every
  read path anyway.
- It is idempotent and safe across processes (the row is simply already gone in
  the second sweep) and runs through `RetryOnBusy`, since a sweep can overlap a
  bot turn's writes.
- Tuning: `OPEN_CHAT_SESSION_SWEEP_INTERVAL` (a Go duration, default `1h`;
  `off`/`disabled`/`never`/`0` disables the sweeper). An unparsable value logs and
  falls back to the default instead of silently changing behaviour.
- `backend/database/session_cleanup_test.go` pins it: only rows past the grace are
  removed (unexpired and inside-grace rows survive), the sweep is idempotent, a
  cancelled context aborts before writing, the interval knob's parsing, and the
  background loop actually deletes a stale row and stops when told.

#### Message history is pruned, not archived forever (ci#850 retention)

Message rows are the bulk of the SQLite volume (see the ci#850 notes above): a
long-lived chat kept every message it ever exchanged - text, reasoning and the
full tool-call payload - forever, because nothing in any read path ever deletes a
message and the UI only pages back through recent history. This prune is the
retention/archival half of the ci#850 work (the busy timeout + WAL maintenance and
the hot-path indexes are the other half).

- `database.StartMessageRetentionSweeper` (`backend/database/message_retention.go`)
  prunes from the server process: once at startup, then every interval until the
  stop function (deferred in `runServer`) is called - the stop is blocking and
  idempotent, so no prune can outlive the server (the ci#811 teardown contract).
- `database.DeleteExpiredMessages` deletes messages older than the retention
  window (default 90 days) while always keeping the newest
  `HotMessageFloorPerChat` (200) messages of **every** chat, however old the chat
  is. The floor is what keeps an old-but-active chat's recent context - and
  `chats.latest_message_id` - alive.
- The delete is **unscoped** on purpose: `Message` embeds `Model`, so a plain
  `Delete` would only stamp `deleted_at` and leave the row's bytes behind - exactly
  the growth this exists to stop. Soft-deleted messages are invisible to every read
  path anyway.
- Each transaction deletes at most `MessagePruneBatchSize` (500) rows and the loop
  stops on a short batch, so a large backlog is reclaimed incrementally instead of
  in one long write holding the SQLite write lock. It runs through `RetryOnBusy`,
  since a prune can overlap a bot turn's writes, and a pass that actually removed
  rows ends with a truncating `CheckpointWAL` so the space returns to the
  filesystem instead of only to the freelist.
- Tuning: `OPEN_CHAT_MESSAGE_RETENTION_WINDOW` (a Go duration, default `90d`),
  `OPEN_CHAT_MESSAGE_RETENTION_INTERVAL` (default `6h`) and
  `OPEN_CHAT_MESSAGE_PRUNE_BATCH_SIZE` (default `500`). `off` / `disabled` /
  `never` / `0` disables the window or the interval; an unparsable value logs and
  falls back to the default instead of silently changing behaviour.
- `backend/database/message_retention_test.go` pins it: the per-chat hot floor
  (old messages pruned, the newest N survive, a recent message is never a
  candidate), the batching, the disabled and nil-db no-ops, a cancelled context
  aborting before writing, the knob parsing, and the background loop actually
  pruning and stopping when told.


#### A message sent into a running chat is queued, never dropped (ci#656)

A message sent while the chat was already running a bot turn used to disappear:
the enqueue clobbered the live turn's task under the stable per-chat task ID
(`bot-reply:<chat>`), so the running turn was cancelled and the new message was
never answered. `POST /api/v1/chats/{chat_uuid}/messages/send` now takes an
explicit `delivery_mode`:

- **`queued` (default, also when the field is omitted)** - the live turn keeps
  running and the new reply is appended *behind* it under a per-message task ID
  (`bot-reply:<chat>:<message>`). Every message is answered and the work the
  live turn already did (files, commits) is kept.
- **`interrupt`** - the explicit "send now" action cancels every reply task of
  the chat (live turn included), voids parked confirmable actions and stops the
  auto-continuation loop for the chat, then starts the new reply immediately
  under the stable per-chat task ID.
- An unknown `delivery_mode` is **rejected with 400** (`Invalid delivery_mode`)
  instead of being silently guessed.

When no turn is live, any queued reply for the chat is replaced by the newest
message, so an idle chat never accumulates stale replies. The idle and interrupt
paths deliberately reuse the stable base task ID (they clear the chat's tasks
first, so a re-enqueue cannot collide with an asynq-archived task); only a reply
queued behind a *live* turn needs a per-message ID because the live turn keeps
the base ID.

`GET` interaction status (`backend/api/chats/InteractionStatus.go`) exposes
`queued_replies` - how many messages are queued behind the chat's live turn - so
the transcript can render persisted-but-pending messages instead of them looking
lost. It is `0` when no turn is live: a single pending reply is just the next
turn waiting for a worker, not a message sent into a running chat.

Plumbing: `backend/workqueue/bot_reply.go` carries `BotReplyDeliveryMode`,
`ParseBotReplyDeliveryMode`, the pure `DecideBotReplyEnqueue`,
`EnqueueBotReplyWithDelivery` / the `EnqueueBotReply` default, `BotReplyQueuedCount`
and the liveness-injected `enqueueBotReplyWithDelivery` core (the `turnLive` flag
is injected because a task waiting in the queue is *pending*, not *active*, so a
unit test cannot otherwise reach the queue-behind branch).

Regression tests: `backend/workqueue/bot_reply_test.go`.

Frontend wiring lives in `frontend/packages/ui/src/lib/delivery-mode.ts` (pure
helpers: `buildSendMessageBody`, `isBotReplyDeliveryMode`, `shouldOfferSendNow`,
`queuedReplyHintLabel`; unit tests in `delivery-mode.test.ts`). The composer
shows **Send now** next to Stop while a turn is live and the composer has
content; it sends the draft with `delivery_mode: "interrupt"`. A plain send
omits the field entirely, so the request body is unchanged from before ci#656
and the server default (`queued`) still applies. A message whose reply is
parked behind the live turn renders a pending note under its bubble - the
predicate stays the transcript's existing `isQueuePendingMessage`
(`integration/lib/status-indicator.ts`) and the helper only names the label, so
the two cannot drift apart. Deriving that state from the message's own
persisted marker keeps the render correct after a reload, where an in-memory
"was this queued?" flag would be gone.

A queued reply that asynq retires instead of running - retries exhausted, a
`SkipRetry` failure or a revoked/archived task - used to leave its marker behind
as well: none of the clear hooks above fires for a task that is never consumed,
so the message kept rendering as pending until the user happened to send again.
`backend/queue/drain` closes that path with `BotReplyTerminalDrain`, an
`asynq.ErrorHandler` wired into both asynq servers (`cmd/server.go` embedded
worker, `cmd/worker.go` standalone worker) as `queue.BotReplyTerminalDrain(DB)`.
It acts only on a terminal `bot:reply` failure and clears the marker of the
failing task's own message only - a newer reply queued behind the same chat
keeps its own marker - and is a strict no-op on a retryable failure (the reply
may still be consumed), on other task types and on missing inputs.

Regression tests: `backend/queue/drain/bot_reply_test.go` (terminal
classification, plus the drain, the retryable no-op and the isolation cases
against sqlite).

#### Chat-state filters are applied server-side, before pagination (ci#761)

The sidebar's state filter (`idle`, `active`, `finished`, `failed`,
`needs_confirmation`) used to run at render time over the chats that happened to
be loaded, so an active interaction older than the first page looked like it did
not exist: the UI showed "no active chats" while the server had several.

`GET /api/v1/chats/list` now takes a `state` query parameter (comma-separated
and/or repeated, case- and space-tolerant; an unknown value is **rejected with
400** through `parseChatStateFilter` / `ErrInvalidChatStateFilter`). A chat's
state is not a SQL predicate - it depends on live queue activity, pending user
confirmations and the newest message - so a request carrying `state` does not use
the SQL-paginated path at all: `listChatsFilteredByState`
(`backend/api/chats/ChatStateFilter.go`) resolves the state of the newest
`ChatStateFilterScanLimit` (1000) chats matching every *other* filter with
`ResolveChatStates`, paginates only the matching subset itself and reports
`total` / `total_pages` for that subset, so one bounded request still finds an
old active chat without pressing "load more".

Semantics that must not drift:

- `ResolveChatStates` is the same resolver `GET /api/v1/chats/states` and the
  single-chat interaction status use, so the server-side filter and the badge the
  UI polls can never disagree. A chat whose state cannot be resolved counts as
  `idle`, so the `idle` filter surfaces it.
- Pinned chats stay subject to the filter: a matching pin is prepended to the
  first page and deduplicated by id (like the ci#512 top-up), a non-matching pin
  (pinned + `finished` while filtering `active`) stays hidden.
- The asynq inspector is optional in this path: without it, chats whose state
  depends on the live queue resolve to `idle` instead of failing the whole list.
- A request **without** `state` keeps the previous code path exactly. It now also
  carries the resolved state per row (`ListedChat.state`), which is what the
  sidebar badges from until the `/chats/states` poll overlays the live value.

Frontend (`frontend/packages/ui/src/components/chat/ChatsList.tsx`,
`chat-list-helpers.ts`): `state` is part of the `useSWRInfinite` key through the
pure `buildChatListUrl` helper (kept out of the component so the "the selected
state must be part of the key" behaviour is testable without a browser),
`stateFilter` is in the dependency list of the effect that resets the painted
pages, the render-time state filter over loaded rows is gone and the row badge
falls back to the server-resolved `chat.state`. A request without a selected
state emits no `state` parameter at all, so the default request keeps its exact
previous shape.

Regression tests: `backend/api/chats/ChatStateFilter_test.go` (parsing,
validation, matcher), `backend/api/chats/ChatStateFilterList_test.go` (handler
level: each supported state, totals/pagination across pages, invalid state, pins,
and the state filter combined with the sibling filters and search) and the
"chat list state filter is part of the request key (ci#761)" block in
`frontend/packages/ui/src/components/chat/chat-list.test.tsx`.

### Coding-agent and sandbox documentation is private

The native coding-agent and sandbox documentation, including provider
concurrency limits, automatic consolidation and context budgeting, is owned by
the private `msgmate_coder` integration and lives with its code
(`open-chat-go-msgmate-coder`, `docs/`). It is served to a running coding
interaction through the integration `coder_docs` tool.

This repository keeps only generic material: setup, build, profiles and the
integration contract. The generic extension points an integration uses are the
versioned integration event envelope and sink in the public integration
interface (`docs/integration-events.md`), the integration-owned route
registration (`Definition.RouteRegistrar`) and its own tool/CLI definitions.

### Clone the repository

The core frontend is tracked in-tree under `frontend/` (no longer a git
submodule). Only the public submodules are checked out with the clone (the Go
interfaces, the Python/JS clients and the build-tools). Private repositories
(the private integrations manifest / CI, the Helm chart, the mobile client and
the LLM coding context) are materialized on demand by the profile setup step.
The native coder sandbox set-up script pre-pulls the repository's public
submodules (guarded on `.gitmodules` and on `git` being present) before that
profile step runs, so the agent starts on a fully materialized checkout
(see `backend/nativecoder/devsetup.go`, `DevSetupScript`).

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

- Profiles: `core-only` (default), `default`, `full`, `full-ci`,
  `full-android` and `full-development`. Override with `INTEGRATION_PROFILE`.
- `full-development` is the profile native coder sandboxes pull by default
  (ci#756): it is the batteries-included development set-up — private CI
  tooling, Helm chart, mobile client and the optional LLM coding context — so a
  development agent starts on a fully prepared checkout. Because the private
  profile membership ships from the private `ci` repository, the generated
  coder set-up script retries `full-ci` when the manifest does not know
  `full-development` yet (see *Coding-agent and sandbox documentation is
  private* above). The generated script also pre-pulls the repository's
  submodules (`git submodule update --init --recursive`, guarded on both a
  `.gitmodules` file and `git` being present) before the profile set-up runs,
  so the batteries-included checkouts are materialized before the LLM starts
  working (see `backend/nativecoder/devsetup.go`, `DevSetupScript`).
- The per-repo default is configurable per bot or interaction: a
  `coder_dev_setup` map may carry a `profiles` table next to its `profile`
  default (`profiles: {msgmate-io/open-chat-go: full-development,
  msgmate-io/open-chat-go-mobile: full-android}`). The longest matching
  repository prefix wins and a repository outside the table keeps the declared
  default, so one bot config can serve several repositories with different
  development set-ups instead of forcing every interaction onto the same one.
- `backend/nativecoder/devsetupmatrix.go` is the audit surface of that per-repo
  default: `DevSetupMatrixPlan` resolves what a ci.msgmate.io coding interaction
  on each configured repository actually gets (sandbox palette variant, profile,
  submodules, extra checkouts), `ValidateDevSetupMatrix` lists defects and
  `DevSetupMatrixGaps` names the repositories that still start half-set-up and
  need a `coder_dev_setup` declaration. `RenderDevSetupMatrixMarkdown` renders the
  operator table; the unit tests keep the public profile catalogue, the per-repo
  default table and the sandbox palette from drifting apart — every repository
  the palette maps a ci.msgmate.io trigger to must carry a per-repo entry of its
  own (`backend/nativecoder/devsetup_perrepo_test.go`), so a client, integration
  or toolchain repository never inherits the monorepo's batteries-included
  `full-development` set-up by accident.
- The map form of `coder_dev_setup` accepts a `setup_commands` escape hatch for
  repositories whose set-up cannot be expressed as an integration profile: a
  non-empty `setup_commands: [ ... ]` list is rendered verbatim as the script
  body (`set -e` fail-fast and the `[ci#756 dev-setup]` marker stay, the
  generated profile/submodule/bootstrap body is skipped entirely — `profile`,
  `submodules`, `repos` and `commands` are ignored while it is set). The
  operator keeps full control of the command text, so nothing is auto-generated
  and nothing is guessed; the environment persistence lines
  (`SandboxEnvSetupLines`, ci#739) still apply so the toolchain PATH survives
  into the agent's later tool-call shells. This ships in
  `backend/nativecoder/devsetup.go` (`ResolveDevSetup`, `DevSetupScript`).
- `backend/nativecoder/devsetupfleet.go` is the per-bot / per-use-case audit of
  that set-up: `CoderDevSetupFleet` lists which coder bot runs which use-case
  against which repository, `DevSetupFleetPlans` resolves the plan each bot
  actually gets (explicit `coder_dev_setup` or per-repo default) and
  `DevSetupFleetGaps` names the bots that would still start half-set-up (a
  repository no plan covers, or a profile outside the shipped catalogue).
  `RenderDevSetupConfigFragment` prints the non-secret `coder_dev_setup` block to
  paste into a bot config or a ci config delta.
- `go run ./backend/scripts/dev_setup_report` renders that audit
  (`--fleet` table is the default, `--matrix` for the per-repo table, `--config`
  for the config fragment, `--format=json` for machine consumption). `--check`
  exits non-zero when any coder bot has a coverage gap, so a CI job or a config
  publish can refuse to ship a half-configured fleet. The report is read-only:
  it provisions no sandbox, makes no network call and prints only bot names,
  repository slugs and profile names — never a token, kubeconfig or private
  module path. Add a new coder bot there when you add it to the runtime.
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

### Integration module PR CI (ci#600)

Integration repositories (`open-chat-go-msgmate-coder`, `open-chat-go-docker-sandbox-integration`, …)
are **not standalone Go modules**: their `go.mod` uses relative `replace` directives into the
monorepo layout (`replace backend => ../../../backend`), so a plain `go build ./...` inside the
integration checkout fails on the first import. That is why they historically had no PR CI — and
how a phase-PR train could leave an integration `main` that no longer compiled while the pin was
perfectly fresh.

Each integration repo gets a five-line caller workflow that reuses
[`.github/workflows/reusable-integration-module-ci.yaml`](.github/workflows/reusable-integration-module-ci.yaml):

```yaml
name: CI
on: [pull_request, push]
jobs:
  module:
    uses: msgmate-io/open-chat-go/.github/workflows/reusable-integration-module-ci.yaml@main
    with:
      workspace_path: clients/integrations/msgmate_coder_integration
    secrets: inherit
```

`workspace_path` must be the `path` the integration is declared with in
[`integrations.yaml`](../integrations.yaml). The reusable checks out the calling (PR) repo and
the `open-chat-go` workspace, then runs
[`development/scripts/integration_module_ci.sh`](scripts/integration_module_ci.sh), which
materializes the profile workspace, drops the PR checkout at `workspace_path` and runs
`gofmt -l`, `go vet ./...`, `go build ./...` and `go test ./...` with `GOWORK=off` — i.e. exactly
how a consumer builds the module. Optional inputs: `open_chat_ref` (default `main`), `profile`
(default `core-only`), `run_tests` (default `true`).

The CI itself is guarded by
[`.github/workflows/integration-module-ci.yaml`](.github/workflows/integration-module-ci.yaml):
`validate` shellchecks/YAML-checks the script and both workflows on every PR touching them, and a
manual `smoke` dispatch runs the reusable end-to-end against any integration repo
(`integration_repository` / `integration_ref` / `workspace_path` inputs).

Run the script by hand:

```bash
INTEGRATION_SRC=/path/to/integration-pr-checkout \
WORKSPACE_PATH=clients/integrations/msgmate_coder_integration \
INTEGRATION_PROFILE=core-only \
bash development/scripts/integration_module_ci.sh
```

The complementary *pin-freshness* guard (does a pinned SHA still exist at the upstream tip) is
`development/scripts/check_pin_freshness.py`, and the *frozen workspace build* guard (does the
pinned tree still compile) is `development/scripts/check_frozen_workspace.py`.

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


### Running the development stack on PostgreSQL (ci#878)

The development stack runs on SQLite by default. A PostgreSQL service is shipped
in `docker-compose.yaml` behind the `postgres` profile, so it stays off unless
you ask for it; `docker-compose.postgres.yaml` supplies the matching backend
environment. Enabling it is one command:

```bash
docker compose -f docker-compose.yaml -f docker-compose.postgres.yaml \
  --profile postgres up
```

To make it the default for your shell (still SQLite when you unset it):

```bash
export COMPOSE_FILE=docker-compose.yaml:docker-compose.postgres.yaml
export COMPOSE_PROFILES=postgres
docker compose up
```

The service listens on host port `5433` (to avoid clashing with a host
PostgreSQL on `5432`) and persists to the `postgres_data` volume; reset it with
`docker compose ... down -v`. Default credentials are `openchat:openchat` and are
overridable with `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`. The backend
connection defaults to
`postgres://openchat:openchat@postgres:5432/openchat?sslmode=disable`; override
it with `DB_PATH` (and `DB_BACKEND=postgres`) if you point at an external server.

Both backends are kept interchangeable: the code must produce identical
behaviour on SQLite and PostgreSQL. Two rules follow from that:

* JSON columns keep working when written as `json.RawMessage`/`[]byte`. On
  PostgreSQL an `exec`/`simple_protocol` pgx query mode encodes those parameters
  as `bytea` and every `jsonb` write fails with SQLSTATE 22P02, so
  `database.ensureJSONSafeQueryMode` repairs such a DSN on startup. Prefer the
  `database.JSONRaw` type for JSON columns the core writes directly (it encodes
  as text on PostgreSQL and BLOB on SQLite).


### Switching branches after a dev build (ci#756)

`docker compose up` runs tooling that regenerates two **tracked** files:

- `integrations.lock.json` — the (non-frozen) `prepare`/`sync` re-pins it to the
  commits currently checked out under `clients/integrations`, which drift ahead
  of the committed lock;
- `backend/server/swagger.json` — embedded with `go:embed` and refreshed by every
  backend rebuild.

Left modified, they make `git switch`/`git checkout` refuse with "local changes
would be overwritten". The dev tooling now restores both automatically: the
integration-sync entrypoint restores the lockfile after `prepare`/`sync`, and
the backend rebuild restores the swagger snapshot after compiling (the built
binary still embeds the freshly generated documentation). If an older checkout
already left them dirty, run the host-side helper once:

```bash
development/scripts/restore_generated_artifacts.sh
```


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

### Private integration documentation pages

Every **private** integration ships a compact, self-contained documentation
page set (Overview / Configuration / Operations) declared through
`FrontendDocs`; the pages, assets and `open-chat.json` examples live in the
integration repository and are never mirrored into this public repo. See
`frontend/pages/docs/content/integration-development.mdx` ("Private integration
documentation pages") for the page-map wiring, the `FrontendDocs` block and the
placeholder-only configuration example rules.

### Chat extensions (cross-integration chat UI)

Chat extension registrations (message inputs, details views, pre-start selectors) live in the integration repo too. Each page set declares an optional `"extension": "<entry-file>"` in `integration.frontend.json`; when linked, the manager regenerates `frontend/integrations/extensions.gen.ts`, which the public app imports at startup so registrations happen globally (the chat page does not import integration code directly). Cross-integration dependencies must go through the registry lookup (`resolveChatUIExtension("opencode")`), never through cross-repo relative imports.

#### Local dev / hot-reload loop

- Run `openchat-integrations frontend --profile <profile>` (or the higher-level `prepare`); it creates the page-set symlinks and the per-checkout `frontend/node_modules` anchor automatically. Re-run it whenever a page set is added or renamed, then restart the Vite dev server.
- The in-repo `frontend/` `npm run dev` / `npm run build` scripts already set `VIKE_CRAWL='{"git":false}'`, so gitignored page-set symlinks are discovered on every host; do not remove that env prefix.
- Inside the dev compose this happens automatically at container start (`integration-sync` service).
- The `integration-sync` service repairs and then drops privileges to the owner of the bind-mounted checkout, so the generated integration clones, `.integrations` cache/markers and frontend symlinks stay owned by you (git no longer reports them as "dubious ownership"). If an older run left them root-owned, the next `docker compose up` heals them; `sudo development/scripts/fix_dev_permissions.sh` does the same without starting the stack.
- `frontend/integrations/extensions.gen.ts` is generated (gitignored). `frontend/scripts/ensure-extensions-gen.mjs` creates an empty placeholder before `npm run dev`/`build`/`install`, and the manager overwrites it with the selected profile's extension imports.
- Note: the `frontend`/`storybook` containers still run as root, so they own `frontend/node_modules` and `frontend/node_modules/.vite`; when building the frontend on the host afterwards, `chown` that directory first.

### Second CI deployment: `ci2` - monitoring / recovery agent (ci#658)

The `ci` deployment (`msgmate-io-ci`, https://ci.msgmate.io) is a full dev
environment. ci#658 asks for a **second** deployment, `msgmate-io-ci2`
(https://ci2.msgmate.io), with a-most the same setup and config - same image and
release flow, same SQLite volume setup, same resources - differing only in that it
configures a **small sub-set of coding-bot capability**: an agent that watches and
monitors the `ci` server and is able to *debug, recover and monitor* it when it
crashes. `ci2` is therefore the deployment that must **not** be the crashed one,
and it holds the credentials to reach `ci`. The emergency/monitoring agent is
started **before** a deployment rolls, or manually from CI when monitoring reports
`ci` down.

The restricted profile itself (`backend/nativecoder/recoveryagent.go`: tool
families, probes, triggers, markers, the single-host boundary) and its deployment
half (`backend/nativecoder/recoverydeployment.go`: the delta below, the rendered
Helm overlay, the watchdog wiring and the provisioning checklist) are code, and
both are unit tested. The private chart lives in
`msgmate-io/open-chat-go-helm-chart`. The values file the overlay is applied on
top of is `development/helm/backend/values.msgmate-io-ci.yaml` (`msgmate-io-ci`);
the new one is `development/helm/backend/values.msgmate-io-ci2.yaml`
(`msgmate-io-ci2`). Production
(`development/helm/backend/values.msgmate-io-production.yaml`, https://msgmate.io)
is **never** a valid target and neither the deployment nor the bot can reach it
(`ValidateDeploymentDelta` + `ValidateRecoveryProfile`).

Recorded delta from `ci` (generated: `RenderDeploymentDeltaTable()`); this is
exactly what `ValidateDeploymentDelta` enforces at build time:

| values key | kind | ci | ci2 | why |
|---|---|---|---|---|
| `deployment.name` | identity | msgmate-io-ci | msgmate-io-ci2 | second deployment, same setup: it must NOT be the crashed one |
| `ingress.host` | identity | ci.msgmate.io | ci2.msgmate.io | new public host; the recovery bot runs here, it watches ci.msgmate.io |
| `runtime.PUBLIC_BASE_URL` | identity | https://ci.msgmate.io | https://ci2.msgmate.io | interaction links and capability markers point at ci2, not at the watched host |
| `ingress.tls.hosts.monitored` | same-as-ci | ci.msgmate.io | ci.msgmate.io | the watched host is an ingress route of the new release |
| `coder.fullCoderEnabled` | capability | true | false | only a small sub-set of coding bot capability is configured here |
| `recoveryAgent.enabled` | capability | false | true | the restricted monitoring/diagnose/recover profile is what runs on ci2 |
| `recoveryAgent.tools` | capability | full coder tool set | coder_bash,coder_read,coder_list,chat_send_message,ssh_exec_command_run_short,service_restart | narrow, prefix-checked subset (see ValidateRecoveryProfile) |
| `recoveryAgent.maxRecoveryAttempts` | capability | 0 | 2 | bounded restart budget per incident, then escalate to a human |
| `recoveryAgent.monitors` | capability |  | ci.msgmate.io | exactly the crash target; production is never in this list |
| `recoveryAgent.credentialRef` | credential-ref |  | ci-recovery-ssh | runtime secret-store reference to reach ci.msgmate.io (never a value) |
| `watchdog.schedule` | watchdog |  | */5 * * * * | health watchdog that dispatches the recovery agent on a crash |
| `watchdog.dispatchTrigger` | watchdog |  | monitoring_crash | same trigger the CI job uses when monitoring reports the server down |
| `image.repository` | same-as-ci | same | same | same image/release flow as ci - the new deployment is not a fork |
| `persistence.storageClass` | same-as-ci | same | same | same SQLite volume setup, strategy Recreate (see DEVELOPMENT.md) |
| `resources` | same-as-ci | same | same | an a-most-the-same copy; capacity work is tracked separately |

1. Create the msgmate-io-ci2 release from the msgmate-io-ci values with the msgmate-io-ci values file as base (development/helm/backend/values.msgmate-io-ci.yaml).
2. Apply the rendered overlay development/helm/backend/values.msgmate-io-ci2.yaml (RenderCI2HelmValues) so name, host and PUBLIC_BASE_URL point at ci2.msgmate.io.
3. Store the SSH/service credential for ci.msgmate.io in the runtime secret store under ci-recovery-ssh and wire only the reference into the deployment env (never an inline value).
4. Confirm the image, storage and resource rows are still pinned to the ci values (no fork of the release flow).
5. Enable the watchdog (*/5 * * * * poll of https://ci.msgmate.io/api/v1/metrics/health) and let a failing check dispatch the recovery agent with trigger monitoring_crash.
6. Run the recovery agent once manually from CI (trigger manual) against a healthy ci.msgmate.io and confirm every probe reports verbatim output.
7. Run it once with trigger pre_deploy immediately before the next deployment, so the bot is already attached while the release rolls.
8. Re-check that neither the deployment nor the bot can reach chat.msgmate.io (ValidateDeploymentDelta + ValidateRecoveryProfile).


The complete config record of the new deployment is the rendered overlay
`backend/nativecoder/testdata/ci2-helm-values.yaml` (`RenderCI2HelmValues`); a unit
test asserts the committed file equals the render byte for byte, so the deployment
config can not drift from the code and every change is reviewable in a PR diff. It
carries **no credential value**: `credentialRef` (`ci-recovery-ssh`) is the runtime
secret-store reference used to reach `ci`, wired into the deployment env as a
reference only.

The crash watchdog polls https://ci.msgmate.io/api/v1/metrics/health every 5
minutes (`*/5 * * * *`). On a failing check it dispatches the recovery agent with
the same trigger CI uses (`monitoring_crash`) and the profile's recovery role;
startup paths are `before_deploy` while a release rolls, `manual` from CI,
`monitoring_crash` from the watchdog and `incident_event` from alert ingestion. The
bounded restart budget (`maxRecoveryAttempts: 2`) restarts the service at most
twice per incident and then escalates to a human. The command surface is the
prefix-checked subset `coder_*`, `github_*`, `hive_mind_*`, `mcp:`, `chat_*`,
`ssh_*` and `service_*` (`allowedToolPrefixes`).

Provisioning checklist (generated: `RenderCI2ChecklistMarkdown()`):


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

### Bot/agent runbooks are private, never in this repository

This guide is **human-facing only**: setup, build, run, test, deploy,
contributing and public architecture. The operational runbooks for the
development bots - how the lead and consolidation agents moderate a fleet, how
the coding agents are admitted, budgeted, compacted and continued, the PR label
policy and the CI-failure routing - are **not** kept in this repository.

They live in the private integration that owns the agents
(`msgmate-io/open-chat-go-msgmate-coder`, `docs/agent/`) and are served to a
native `msgmate_coder` interaction at runtime by the integration's `coder_docs`
tool, which is part of the default coder tool surface (`action: "list"` to see
the pages, `action: "read"` to fetch one). Nothing is embedded or mirrored
here, so the guidance reaches the agent that needs it without shipping to the
public repo. That repository's README documents the mechanism.

Maintainers who need the runbooks get access to the private integration
repository (or ask a coder agent to quote a page through `coder_docs`). Do not
add agent runbooks, playbooks or an agent-doc tree back to this repository:
`go test ./docguard/` in `backend/` fails when agent-only documentation
reappears publicly.

### Native bot runtimes on the ci deployment (ci#584)

The development bots of the ci deployment run on the native `msgmate-coder`
runtimes. The flip lives in `backend/nativecoder/rollout.go`: the fleet moves
family by family (plan, then repl/coding, then review, then admin, then the
coordinating `msgmate-coder` lead last), every family keeps the retired opencode
runtime configured but out of rotation for the rollback window, and the opencode
integration is only dropped once every family has been native and stable and a
rollback was proven on a non-critical family. `RetirementBlockers` is the
machine-checked go/no-go for that last step and `RenderNativeBotRolloutMarkdown`
renders the table the private runbook shows. Runtime selection in a
runtime-selector message maps the older bot names onto the native runtime via
`LegacyRuntimeBotAliases`, which is derived from the same registry, so retiring a
family is a one-line change with a test behind it.

The API side of the same rollout is staged by `NATIVE_CODER_CUTOVER`
(`fallback` | `native` | `retired`, unset means `fallback`) and implemented in
`backend/api/msgmate/runtime_cutover.go`:

- `fallback` keeps the opencode bots offered as the immediate rollback.
- `native` is the canary window: the fleet is served natively, the opencode
  choices stay visible until the flip is signed off.
- `retired` drops every non-native runtime choice from a runtime-selector
  payload and refuses to resolve an external `chat_backend: opencode`, so a
  stale chat config or a reinstalled integration cannot route a chat back onto
  the retired runtime.

The stage is read per request, so a rollout (or a rollback) is an env change on
the deployment, no rebuild and no data migration. `publish-open-chat-config.yaml`
(vault-sync, `DRY_RUN=true` first) is the path for that change. The ci deployment
profile (`docker-compose.ci.yaml`) forwards `NATIVE_CODER_CUTOVER` and the
rollback window `MSGMATECODER_NATIVE_ROLLBACK_WINDOW` into the backend container;
`backend/nativecoder/cutoverenv_test.go` guards that wiring, because a missing
forward makes `retired` unreachable no matter what the vault says.

### Native coder / opencode capability parity (ci#394)

The second half of the replacement goal is *capability*: flipping the fleet is
pointless while the native `msgmate_coder` agent still misses parts of the
opencode surface it replaces. `backend/nativecoder/opencodeparity.go` keeps that
gap list as data - the nine gaps of ci#394 with their sequencing phase
(`now`/`next`/`later`), status (`open`/`partial`/`closed`), the coder tool that
closes the gap, the opencode capability it replaces, the tracker tickets and a
one-line note about the current shortfall:

- `ValidateOpenCodeParityPlan` fails on a malformed registry (duplicate or
  unknown ids, unknown phase/status, a tool claimed by two gaps, or a `closed`
  gap naming a tool the coder does not ship), and the unit tests call it for the
  shipped registry.
- `RetirementParityBlockers` is the capability gate: while any blocking gap is
  open it returns one line per gap, and `RetirementReadiness` composes it with
  the mechanical `RetirementBlockers` above, so "can we drop opencode?" is a
  single call. Both must be empty.
- `ParityRetentionEntries` turns the open gaps into the `RetainedParity` reason
  the migration state requires, so a family still on opencode because of a gap
  is a recorded exception instead of a silent default.
- `RenderOpenCodeParityMarkdown` and `OpenCodeParityJSON` render the same data
  for the private runbook and for run evidence; closing a gap in prose without
  closing it in the registry no longer matches what the gate reports.

While a gap is still open the agent needs to know what to do *instead* of the
opencode capability it misses, and that mitigation is data too:
`backend/nativecoder/parityfallbacks.go` keeps one fallback line per gap id
(`ParityFallbackFor`, `ParityFallbackGuidanceLines`,
`RenderParityFallbackMarkdown`, `ParityFallbackJSON`). Each line names at least
one tool of `CoderSurfaceTools()`, so the guidance can only point at a tool the
coder actually ships, and `ValidateParityFallbackGuidance` /
`UncoveredParityGapIDs` fail when a new gap is registered without a mitigation, a
mitigation names no shipped tool, or a *closed* gap still carries a workaround
(that would tell the agent to avoid a capability the coder already has). That
keeps ci#394 honest in both directions: the registry says what is missing, the
fallback table says how the coder copes until it is added.

Gap 1 (`todo_list`) is being closed from the core side first:
`backend/nativecoder/todoplan.go` holds the per-chat plan state behind
opencode's `todowrite`/`todoread` (`CoderTodoStore.Write`/`Read`/`Clear`),
mirroring the opencode contract - four statuses, at most one step in progress,
stable positional ids, a plan cap, a revision that increments per accepted
write, structured metadata (`Metadata`) and a deterministic markdown renderer
(`RenderCoderTodoPlan`) for the chat view. `NormalizeCoderTodoItems` is the
single validator, so a rejected write is classified by error value rather than
by parsing a message. The gap is `partial` until the `coder_todo_write` /
`coder_todo_read` tools on top of this state ship: the registry only reports the
surface the agent can actually call.

Sequence the work as ci#394 suggests: the cheap gaps first (visible todo list,
allow-for-this-session trust, streaming/background shell), then patch/multi-edit,
image and structured reads, session compaction, and only then the large pieces
(sub-agents, code search/LSP, per-workspace MCP).

### A/B validation of the opencode -> native flip (ci#583, umbrella ci#585)

Before a bot family is flipped from its opencode-era bot to the native
`msgmate-coder` replacement, the two are run side by side on the same tickets
and compared axis by axis. That evidence is data, not prose:
`backend/nativecoder/abvalidation.go` holds the comparison axes (ticket
implemented, PR opened with CI labels, auto-sandbox used, no `chat_backend`
fallback, auto-continue completed the turn, no parked turn) and the gate over the
recorded paired runs.

- `ABValidationCases()` is the empty state ci#583 starts from; `ABValidationTemplate()`
  is the fill-in skeleton (one row per family and run, pairing pre-filled), and
  `ValidateABValidationPlan` rejects a plan with an unknown family, a family
  paired against the wrong runtime or a foreign legacy bot, an unknown axis, or a
  family with no paired run.
- `ABValidationFamilyBlockers` / `ABValidationFlipAllowed` gate a single family
  (too few runs, a missing outcome, or a native regression on an axis the legacy
  run passed); `ABValidationBlockers` adds the fleet-wide ci#581 parity gaps, so
  "may we start the rollout?" is one call.
- `ABValidationSignOff` derives the `ABValidationDone` field of the ci#585
  retirement gate from the evidence via `ABValidatedRetirementState`, so the
  umbrella flag is a fact about the paired runs instead of a hand-set boolean.
- `MigrationUmbrellaSteps` derives the ci#585 checklist (parity, config delta,
  A/B, rollout, retire) from the same registries, so the umbrella cannot claim
  progress the repository does not support; a blocked step carries the first
  blocker as its detail.

`go run ./backend/scripts/ab_validation_report/` renders the table, with
`--format=json|text` and `--umbrella` for the checklist; it takes JSONL paired
runs (blank lines and `#` comments ignored) and is read-only - it never
dispatches a bot and never touches the deployment.

## Native agent dispatch - retiring the workflow-only dispatch route (ci#757)

The `trigger-open-chat-coding-interaction.yaml` workflow in the private
`msgmate-io/open-chat-go-ci` repository is the *legacy* way for a lead agent to
start another coding agent: it shells out through `coder_bash`
(`gh workflow run`) and relies on GitHub to deliver the trigger. It stays
supported as the reproducible, auditable fallback, but it is **deprecated as the
primary route** for the coordinating `msgmate-coder` agent.

The native route is the in-server agent-dispatch surface documented in
`backend/api/bots/AgentDispatch.go`, exposed to the native coder (and through the
hive-mind integration) as the `hive_mind_agent_*` tool family. It mirrors the
workflow inputs (task message, repo/issue tags, `autonomous`,
`approve_confirmations`, auto-continue + completion/human markers,
workspace/sandbox) and adds the continue/stop lifecycle, so a lead agent starts,
observes, steers and stops peers without a GitHub round-trip and without a
second, structurally different trigger path to keep in sync:

- `hive_mind_agent_dispatch` - start a named bot with a task, tags, autonomous
  and auto-continue options; returns the `chat_uuid` to watch.
- `hive_mind_agent_list` / `hive_mind_agent_get` - list the in-flight and
  finished interactions and read one transcript.
- `hive_mind_agent_continue` / `hive_mind_agent_stop` - nudge or stop a peer.
- `hive_mind_observe_list` / `hive_mind_observe_get` - snapshot what other
  interactions produced (a plan, a PR, a blocker).
- `hive_mind_state_get` / `hive_mind_state_set` - durable state that survives a
  reaped sandbox.
- `hive_mind_influence` - queue an intervention on an observed interaction.

### Recalling old chat context

Past development chats stay searchable through the interaction-history tools
(`mcp:open-chat-opencode-mcp:openchat_list_interactions`,
`openchat_get_interaction`, `openchat_search_interactions`,
`openchat_search_interaction_messages`), so "what did we already try on this
issue?" is answerable from inside a dev-agent chat instead of by scrolling the
server UI.

### Enabling the tools selectively

The families are opt-in per bot/chat through the shared-config `tools` key. An
**empty** `tools: []` means "not configured": the key is dropped so the
compiled-in defaults apply (ci#617). To narrow a chat deliberately, list a
non-empty array. Enable the `hive_mind_*` tools (plus the `mcp:` interaction
history tools) for lead/dev-agent chats; a leaf worker agent that only
implements one issue does not need the dispatch family.

### Migration order

1. Enable and verify the native tools for the lead/dev-agent chats on
   ci.msgmate.io (a dispatch -> observe -> continue/stop round-trip).
2. Keep both routes until the native route is verified live.
3. Then retire the workflow from the lead's prompt/config in one consolidation
   PR and record the version that flipped it, so the change is revertible.

The order above is enforced, not just written down.
`backend/nativecoder/nativedispatchgate.go` makes the retirement a checked fact:

- `LegacyDispatchInputParity()` maps every input of the legacy workflow onto its
  native counterpart, and `LegacyDispatchParityGaps()` lists the inputs for which
  the native route still has no equivalent. A non-empty gap is *why* the workflow
  is still in use, and it blocks the retirement;
- `DevAgentToolProfile(role)` resolves the tool list for a development-agent chat
  by role: the coordinating role gets `NativeDispatchToolFamily` (dispatch,
  list/get, observe, continue/stop) **plus** `ChatContextRecallToolFamily` (the
  `mcp:` interaction-history tools, so a dev chat can search old chat context);
  a leaf role gets context recall only, and an unknown role is an error instead
  of a silent widening of the tool list;
- `NativeDispatchChatBlockers()` / `NativeDispatchBlockers()` report the open
  reasons a chat is not yet on the native route (missing tools, no observed
  round-trip, still referencing the workflow), `NativeDispatchReady()` is the
  gate, and `NativeDispatchEvidence()` / `RenderNativeDispatchEvidenceMarkdown()`
  turn the same blockers into the per-step checklist of this ticket - a step
  cannot show `done` while its blocker is listed.

The read-only job `backend/scripts/native_dispatch_report/` prints that evidence
for a ticket comment (`--format=markdown|json`), the workflow-to-native input map
(`--parity`), and turns the gate into a non-zero exit with `--check`. It reads the
observed fleet as JSON (`NativeDispatchStateJSON`) and performs no network calls:
it never dispatches a bot, never touches the deployment and never prints a
credential - the state is chat metadata (tool list, observed round-trips,
workflow references) only. An empty state reports every step blocked, which is
the state ci#757 starts from.

### opencode retirement umbrella (ci#585)

`backend/nativecoder/opencoderetirement.go` gates the retirement of the opencode
integration on the flipped fleet. `backend/nativecoder/retirementevidence.go`
breaks the same gate down per umbrella step (`OpencodeRetirementEvidence`): each
checklist entry of the ticket gets the status the gate implies and the blocker
lines that belong to it, and `RenderOpencodeRetirementEvidenceMarkdown` renders
that table for a ticket comment. Because the rows are derived from
`OpencodeRetirementBlockers`, a step cannot be reported as signed off while its
blocker is still listed.

The config half of the same umbrella (ci#582) is the *delta* the flip publishes:
`backend/nativecoder/retirementdelta.go` renders the per-bot runtime mutations
from the same flip registry - `OpencodeRetirementFlipDelta`,
`OpencodeRetirementRollbackDelta`, `OpencodeRetirementDeltaJSON` - together with
the ordered apply plan (`OpencodeRetirementDeltaApplyPlan`), which goes through
the vault-sync path `publish-open-chat-config.yaml` with `DRY_RUN=true` first.
`ValidateOpencodeRetirementDeltas` fails unless the rollback delta is the exact
inverse of the flip delta, and `PendingOpencodeRetirementFlipDelta` reports the
entries still outstanding for the observed fleet, so the delta, the gate and the
deployment never disagree. The read-only job
`backend/scripts/opencode_retirement_report` prints the per-step evidence
(`--format=markdown`, `--format=json`) or the flip delta (`--format=delta`) for a
ticket comment; `--require-ready` turns the gate into a non-zero exit for a
scripted check.

The last step of the umbrella (ci#585) removes the integration itself:
`backend/nativecoder/opencoderetirestop.go` renders the *stop delta* -
`OpencodeRetireStopDelta`, `OpencodeRetireStopDeltaJSON` - which disables the
`opencode` chat backend, removes its dev bots and then removes the integration,
in that order, through the same vault-sync path. `ValidateOpencodeRetireStopDelta`
rejects drift: unknown kinds, missing targets/reasons, duplicates, a native
runtime named as a stop target, or a bot the registry knows but the delta forgets.
A stop is only reported as complete once `OpencodeRetireStopRecord` (date,
operator, `retired` mode, stopped bots) is present *and* the retirement gate is
open, so the umbrella's final checkbox cannot be ticked while a bot still
resolves to the opencode backend. The report job exposes the same evidence with
`--format=stop` and `--require-stopped` (non-zero exit while it is incomplete).

## Decoupled integration builds (ci#423)

Once an integration's shipped packages no longer import `backend/...` (verified by
`development/scripts/check_integration_decoupling.py`), it can be built against its
minimal compile inputs only:

- pass `decoupled: true` to `.github/workflows/reusable-integration-module-ci.yaml`, or
- set `DECOUPLED=true` when calling `development/scripts/integration_module_ci.sh` directly.

That path skips profile materialization, so the compile inputs are the integration
module plus the pinned `clients/go_integration_interface` module, and it enforces the
decoupling guard as a **hard gate** (`REQUIRE_DECOUPLED` defaults to `DECOUPLED`, so a
decoupled build fails when the module still couples to `backend/...`). It is the
per-integration prebuild precondition from the ci#345 design
(`frontend/pages/docs/content/integration-prebuild-design.mdx`); the migration recipe
for the remaining integrations lives in
`frontend/pages/docs/content/integration-development.mdx`. It is safe to enable
`decoupled: true` the moment the integration's decoupling lands: the guard then
protects against a later `replace backend` cleanup re-coupling the module silently.

### Re-baselining the dependency surface deliberately

`integrations.dep-surface.json` is the committed record of the integrations that are
already decoupled. `development/scripts/check_dep_surface_gate.sh` consumes it on every
pull request:

- it always validates the baseline without a Go toolchain (a malformed or
  wrongly-pinned baseline fails, exit 2/1);
- when a Go workspace is materialized (the Go-test job exports `GOWORK`) it additionally
  measures the integration modules and prints the offending import edge
  (`module: backend/...`) before failing -- and only for a *regression* against the
  baseline, so integrations still being migrated are reported, not failed;
- with no workspace (the toolchain-free integration-module CI job) it validates only and
  says so, and it is a no-op when `integrations.dep-surface.json` is absent.

The baseline is intentionally small: it records only integrations whose shipped
packages no longer import `backend/...` at all. Extend it only in its own commit, for an
integration that is actually fully decoupled, and never to silence a regression:

```sh
python3 backend/scripts/integration_dep_surface.py --write-baseline integrations.dep-surface.json
python3 backend/scripts/integration_dep_surface.py --check-baseline integrations.dep-surface.json  # must be 0
git add integrations.dep-surface.json && git commit -m "ci#423: pin <integration> dependency surface"
```

A coupled integration listed there (or a `backend/...` package it does not record) is a
regression by definition; fix the import instead of re-baselining.
