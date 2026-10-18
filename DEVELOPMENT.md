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

#### Non-required status checks never block a merge (ci#683)

The `main` ruleset requires exactly one context (`Run PR CI Tests and
Deployments`). Any *other* status on the head is informational — most notably the
`private-sandbox-tests` commit status the private sandbox suite sets on the PR
head from its own repository.

GitHub, however, derives `mergeable_state: blocked` — and therefore the CLIs'
`gh pr merge` preflight — from **any** non-success commit status on the head,
required or not. A PR whose only required context was green, which was
`APPROVED` and `mergeable: true`, was reported as

```
X Pull request msgmate-io/open-chat-go#700 is not mergeable: the base branch policy prohibits the merge.
```

while the identical merge through the REST endpoint
(`PUT /repos/{owner}/{repo}/pulls/{n}/merge`) succeeded immediately (the r25
consolidation incident). The ruleset is the source of truth, so both paths now
agree:

- the core auto-consolidation client reads the base branch's ruleset
  (`GET /repos/{owner}/{repo}/rules/branches/{branch}`) and evaluates exactly the
  contexts it requires; a red *non-required* check/status is reported as
  informational, never as "checks are failing", so such a PR stays a ready
  consolidation candidate (`backend/nativecoder`, `requiredchecks.go`);
- the typed `github_pr_merge` tool of `msgmate_coder` re-evaluates a
  "base branch policy prohibits the merge" refusal against the same ruleset and,
  when every required context is green, the review is APPROVED and the only red
  statuses are non-required, completes the merge through the REST endpoint and
  reports `informational_checks`. Any other refusal keeps failing, now naming the
  resolved required/informational state;
- the private sandbox suite no longer sets a failing commit status on PR heads
  (its report link comment and the `main` regression guard are unaffected), so no
  informational status can gate a merge in the first place.

Never wait for or poll CI, deployments or approvals from an agent turn; report
what is pending instead.

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

#### A turn never ends on empty text, and the per-chat tool cap is a snapshot (ci#709)

A native-coder turn that ran dozens of tool calls could end with `state=finished`
and **no `last_agent_text`** while the bot message carried 60+ tool calls: the
work was done but never reported, so it looked lost. In parallel, a chat created
before a cap raise kept aborting at the old cap (100), because the caps were
copied from the bot's default shared config into the chat at creation time and
never re-read.

Both halves are now fixed at the turn choke point:

- Every terminal path of the tool loop records a reason and the turn's counters
  (`backend/api/msgmate/turn_outcome.go`): cap reached, failed-cap reached,
  provider error, cancel, loop guard (`no usable tool call`) and the confirmable
  pause. `ResolveTerminalAssistantText` is the last step before the bot message
  is persisted and guarantees non-empty, human-readable text that states what
  happened and what to do next - a turn that ran tools always says the work is
  kept and how to continue, a stop keeps whatever the turn already said and
  appends the pause marker.
- A reached cap is a typed `toolCallCapError`. It keeps the historic wording
  (`exceeded maximum number of tool calls (N)`) so existing matches and the queue
  task's failure classification keep working, and carries the counters plus the
  user-facing guidance.
- The resolved caps, this turn's consumed/remaining counters and the terminal
  reason are persisted as `tool_call_budget`, `tool_call_usage` and `turn_ended`
  message metadata, so the budget is inspectable instead of only living in an
  error string.
- `backend/nativecoder/toolbudget.go` snapshots the per-chat budget in the chat's
  shared config at creation (`tool_call_budget`), and every turn resolves the
  effective caps as the **maximum** declared by the snapshot, the chat's own
  config, the bot runtime's *current* shared config and the compiled-in default.
  A cap raise on a bot therefore reaches chats that already exist, a declared
  budget is never silently lowered, and the snapshot survives a re-attach / a
  re-provision. The bot's default shared config deliberately stays snapshot-free:
  it is the live source a raise comes from.

Regression tests: `backend/api/msgmate/turn_final_text_test.go`,
`backend/nativecoder/toolbudget_test.go` and the snapshot assertion in
`backend/api/chats/create_test.go`.

### A native interaction that cannot start is reported, never a silent idle (ci#609)

When the sandbox node was saturated, a dispatched native `msgmate_coder`
interaction could stay `state=idle` with `newest_agent_msg=none` **forever**: no
bot message, no `Unschedulable: … Insufficient cpu` provisioning error, nothing
the operator or the external auto-continue dispatcher could act on. The loop just
printed `[loop] state=idle continuations=0 newest_agent_msg=none finished=false`
until it gave up, and the dispatched work looked frozen and lost.

The core now detects that condition from durable state and reports it
(`backend/api/chats/interaction_stall.go`). An interaction is **stalled** when

- it is an interaction chat;
- **nothing is live** for it - no `bot:reply` task active/pending/scheduled/retry
  (including a reply queued behind a live turn, ci#656), and no external chat
  backend reporting a running generation;
- its newest message is still the prompt (the agent never produced a turn after
  it) and does not wait on the user (no pending confirmation - a parked
  confirmation is a decision, never a stall);
- and that prompt has rested longer than the stall window.

`ResolveInteractionStatus` then annotates the deterministic status with
`stalled`, `stall_reason` (`stall_no_first_turn`), `stall_age_seconds` and
`stall_threshold_seconds`, and the trusted agent-dispatch view
(`GET /api/v1/agents/interactions…`) carries the same fields - so the external
dispatcher loop can turn an idle-timeout into a failure naming the interaction
URL instead of stopping silently, and a lead agent can tell a silent freeze from
healthy idle work.

Reading `GET /api/v1/chats/{chat_uuid}/status` (the endpoint the loop polls)
also **posts one visible, persisted bot error** for a stalled chat: `finished` +
`error` metadata with `failure_reason=stall_no_first_turn` and an
`interaction_stall` detail block. The message states the observed age, the
resolved window and what to do (re-dispatch, or continue once capacity is free),
and it makes the interaction resolve as `failed` instead of `idle`. The report is
idempotent (a `interaction_stall_report` marker on the message), so polling the
status cannot spam the transcript.

The stall window is resolved with the ci#620 precedence **deployment env →
per-agent shared config → dispatch metadata → default**:

- `OCI_INTERACTION_STALL_AFTER_SECONDS` (deployment-wide; `0` disables the
  guard);
- `interaction_stall_after_seconds` in the agent's shared config (or in its
  `agent_dispatch` block);
- default `10m`, matching the window the external dispatcher loop observes
  before it gives up.

Regression tests: `backend/api/chats/interaction_stall_test.go`.

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

### Transient tool failures are retried and budget-exempt (ci#688)

A tool body can fail for purely **environmental** reasons: with 16–24 concurrent
agents a per-chat auto-sandbox sometimes could not be provisioned
(`failed to create temporary sandbox: Cloning … error: copy-fd: …`), the host ran
out of file descriptors / disk, or the sandbox/integration host was momentarily
unreachable. Each such blip used to be a hard tool failure, so the agent burned
its failed-tool budget and aborted — and a plan-mode agent then re-cloned and
retried by itself, which made the exhaustion worse.

The core tool loop therefore classifies tool failures (`api/msgmate/tool_failure_retry.go`):

- a recognised **transient** failure (sandbox provisioning, host resource
  exhaustion, connection-level blips — the marker list is deliberately limited
  to infrastructure) is retried with an exponential backoff plus bounded jitter,
  bounded by the generation context so a user stop is never resurrected;
- a transient failure that survives the retries is still reported to the model
  as a failed call, but it does **not** consume the failed-tool budget — the
  total tool-call cap still bounds the turn, so an agent cannot loop forever.
  The model-facing result says so explicitly and suggests a short-wait retry;
- a failure the model can fix (a bad argument, a missing file, a permission
  error) is never retried and always counts against the budget.

The per-call flags (`retries`, `budget_exempt`) travel in the tool-call metadata
and the `interaction_complete` payload (`tool_call_transient_failed_count`), so a
saturated sandbox pool is visible in the transcript instead of only in the
backend log. Both knobs are configurable per deployment:
`OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_ATTEMPTS` (default 3, clamped to 1–8) and
`OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_BASE_DELAY_MS` (default 400 ms,
exponential up to a 5 s cap).

### Scheduler-capacity failures wait for the node, they are not blips (ci#691)

The ci#688 retry policy above classifies an **unschedulable sandbox pod** as a
transient failure, but it backed it off on a millisecond scale — three attempts
within ~1.2 s — which cannot absorb a *node capacity* condition:

```text
sandbox "coder-7b205aa3-…" is not schedulable: Unschedulable:
0/1 nodes are available: 1 Insufficient cpu. no new claims to deallocate,
preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod.
```

The node only frees a sandbox's request when a finished sandbox is reaped or a
peer agent releases its pod — tens of seconds, not hundreds of milliseconds — so
the attempt budget was burned on a condition that could not have changed, and
every agent of a saturated pool did it at the same time. The same finding is what
the sandbox integration's background credential re-provision path reports
(`failed to re-provision credentials for sandbox ssh server … after 5m0s`, the
ci#691 signature; the fail-fast half is
`msgmate-io/open-chat-go-docker-sandbox-integration#29`, the capacity levers are
ci#529 / ci#545 / ci#602).

`backend/api/msgmate/sandbox_capacity.go` (with the capacity backoff in
`api/msgmate/tool_failure_retry.go`) therefore splits the capacity case out of the
blip case:

- a **scheduler capacity rejection** (`isSandboxCapacityFailure`: `insufficient
  cpu` / `insufficient memory` / `is not schedulable` / `unschedulable` /
  `no preemption victims` / `nodes are available`) is retried with a slower,
  still bounded backoff — a linear ramp from `5s` to a `30s` cap, plus bounded
  jitter — instead of the ci#688 millisecond backoff;
- while the node is known to be saturated, the **other** provisioning calls of
  the fleet are held back for the same window (`awaitSandboxCapacityCooldown`).
  The cooldown is process-wide, not chat-scoped: node capacity is shared by every
  interaction the backend serves, so one agent learning that the node is out of
  room is information the whole fleet can use. It is a wait, never a block — it
  is bounded by the generation context (a user stop ends it) and capped at one
  capacity window;
- the failure that reaches the model is its own message
  (`buildSandboxCapacityToolErrorPlaceholder`): the sandbox could not be
  *scheduled* because the node is out of CPU/memory, it does **not** consume the
  failed-tool budget, and the agent should wait or continue with work that needs
  no sandbox instead of re-provisioning in a loop;
- the condition is surfaced separately from a generic transient failure: the tool
  call carries `sandbox_capacity` alongside `retries`/`budget_exempt`, and the
  `interaction_complete` payload reports
  `tool_call_sandbox_capacity_failed_count`, so a saturated node is visible in the
  transcript instead of only in the backend log.

The capacity backoff base is configurable per deployment with
`OCI_MSGMATE_CODER_SANDBOX_CAPACITY_RETRY_BASE_DELAY_MS` (default `5000`, the ramp
is capped at `30s`); the attempt count is the shared
`OCI_MSGMATE_CODER_TOOL_FAILURE_RETRY_ATTEMPTS` of ci#688. This is the application
half of the fix — it makes the loop wait instead of stampeding — and does not
replace the capacity levers themselves (node sizing, the right-sized per-chat
requests of ci#604, the pooled/shared mode of ci#545).

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
`coder_sandbox_max_agents_per_pool` / `OCI_MSGMATE_CODER_MAX_AGENTS_PER_POOL`
(ci#602) when that is smaller. **The deployment lever wins over the per-agent
key**: the shared-config default merge materialises
`coder_provider_max_concurrency: 3` into every native coder agent's config, so
a "config first, env second" precedence pins the whole fleet at 3 forever (that
was ci#620 — the effective admission cap never left the literal default, no
matter what `OCI_MSGMATE_CODER_PROVIDER_MAX_CONCURRENCY` said). Resolution
order is therefore env → per-agent key → pool bound → default, the resolved
source is logged with the cap, and an existing limiter is resized in place: a
raised cap admits more requests immediately, a lowered cap only takes effect as
in-flight requests drain, so a change lands without a redeploy.

The core-side admission controller (`backend/api/msgmate/provider_admission.go`)
admits every provider request of the built-in chat path through that per-key
limiter and logs `provider_admission` with `effective_max_concurrency`,
`in_flight`, `queue_waiting` and the config `source`; the admin-only
`GET /api/v1/admin/provider-admission` route reports the same state for the
whole process (the raw env levers, the resolved policy and one snapshot per
admission key, never key material). The per-agent keys
(`coder_provider_max_concurrency`, `coder_provider_queue_timeout_ms`,
`coder_provider_fallback_on_saturation`,
`coder_provider_fallback_backend`/`_model`/`_endpoint`/`_api_key_env`,
`coder_provider_fallbacks`) mirror the deployment env
(`OCI_MSGMATE_CODER_PROVIDER_MAX_CONCURRENCY`, `..._QUEUE_TIMEOUT_MS`,
`..._FALLBACK_ON_SATURATION`, `..._FALLBACKS`). A fallback of a known provider
with no explicit key env resolves `<PROVIDER_API_KEY>_FALLBACK`, so an operator
only has to export one extra variable to shed load onto a second key. No key —
primary or secondary — is ever exposed: the admission identity and every status
snapshot carry only a truncated hash/length hint. `coder_context` reports the
in-flight count and cap, and the admin-only
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

### Development lead: CI-error relay ingestion and routing (ci#608)

The development lead ingests relayed CI-failure events and routes each one to
the repo's development-agent interactions. The ingest + routing decision lives in
the integration-free `backend/nativecoder` package (`devlead.go`), so both the
core-only and the full build carry it and it is unit-testable without an
integration profile; the integration-side transport/hook that delivers the event
belongs to the base-integration extension (ci#607) and only emits the payload.

Contract and behaviour:

- **Payload** — a small, versioned, secret-free `ci_failure` event (`ci_error` is
  accepted as an alias): `repo` (required), `provider`, `pr_number`, `branch`,
  `sha`, `run_url`, `failing_jobs`, `failure_logs`, `attempt`, `severity`,
  `occurred_at`. A payload advertising a newer `schema_version` is rejected
  instead of being misinterpreted (`ParseCIFailureEvent`).
- **Fix session** — the dedupe/attempt-cap identity is per repo+PR, else
  repo+branch, else repo+commit (`FixSessionKey`); repo and branch are
  normalized (lower-cased, `.git` and `refs/heads/` stripped).
- **Candidates** — the routing primitive `CandidateInteractions` enumerates the
  failing repo's live + recent interactions (a running interaction always
  qualifies, a past one only inside `DefaultDevLeadRecentWindow`, 24h), ordered
  most-recent-first with a UUID tiebreak so routing is deterministic.
  `ResolveLeadInteraction` answers the agent-chooser question "which interaction
  receives this repo's CI-relay traffic?" (running lead, else running, else most
  recent recent interaction).
- **Routing order** (`DevLeadRouter.Route`): (1) `dedupe_existing` when an
  already-open fix session for the same fix-session key owns the attempt — no
  second fix attempt; (2) `escalate_maintainer` when the fix-cycle cap
  (`DefaultFixCycleMaxAttempts`, 3, overridable) is exhausted — no dispatch;
  (3) `append_ongoing` to the running interaction, preferring one that last
  touched the failing branch/PR; (4) `reactivate_past` the recent past
  interaction that produced the change; (5) `spawn_new` a fresh plan→implement
  session on the failing branch.
- **Re-prompt** — `LeadRoutingDecision.Prompt` renders the structured fix
  request appended to the chosen interaction: target identifiers, failing jobs
  and failure output, the routing decision plus a **visible trail** (which
  candidate was chosen and why, which were ignored and why), the
  `<!-- oci-ci-failure-fix -->` dedupe marker, and the **never-poll-CI** rule —
  the agent pushes the fix and stops, the lead reacts to the next relayed event.

### Native coder turns: inline tool calls and leaked compaction transcripts

A provider round can deliver two kinds of output the native coder must never
persist as a turn's assistant text (ci#428, ci#562):

- an **inline tool call** the provider did not map onto the structured
  `tool_calls` field — DeepSeek **DSML** control tokens, the Anthropic/XML
  `<function_calls><invoke name="…">` shape, or the `[[tool_name key="value"]]`
  bracket placeholder some models fall back to. Detection runs against the
  *accumulated* stream (markers are split across deltas, so recognising them
  only once complete would already have leaked the prefix), content is withheld
  from the client, and the call is reassembled and executed after the stream
  ends (`parseInlineToolCalls`). A block from which nothing can be recovered
  fails the turn with `errMalformedInlineToolCall`;
- a **leaked context-compaction transcript** — the model volunteering its own
  bookkeeping, an `<analysis>…</analysis>` block followed by a numbered
  `<summary>…</summary>` block ("Primary Request and Intent", …,
  "Optional Next Step"). It is model bookkeeping, never an answer, so it is
  stripped before the round result is returned; the private `tags=coder`
  benchmark `msgmate_coder_repo_changes_summary` caught exactly that emission
  as a final reply (the summary file was never written and the reply *was* the
  blob). Only a message that **opens** with such a tag (optionally after a
  fence) is treated as a leak — an ordinary answer that merely mentions the tag
  inside real prose is left untouched.

Either shape counts as "no usable text" for the same single retry the empty
post-confirmation continuation already got (ci#428): when a round's only output
was a stripped transcript the loop retries it once instead of finishing on it.
If the retry produces another transcript the turn fails with
`errLeakedCompactionTranscript`, classified as its own
`leaked_compaction_transcript` failure reason so the chat explains it with the
"the model returned its internal work notes; the work already done is kept —
send another message" guidance instead of a generic internal error, and no
empty reply is ever persisted. Recovery lives in
`backend/api/msgmate/inline_tool_call.go` (parsers) and
`backend/api/msgmate/compaction_output.go` (compaction detection/stripping),
with the streaming suppression in `processStreamingResponseReader`.

### Native coder turns: repetition loops (ci#670)

A third degenerate emission is an **unbounded repetition loop**: the model
re-emits the same short line or phrase (`</invoke>`, `...`, `Ok.`, `Write.`, a
degenerate Anthropic/XML inline call) thousands of times. Nothing upstream of
the reader stops it, so a single turn used to persist 150–255 KB of identical
junk into the chat, pollute the next turn's context and burn the whole
completion budget before the turn was cancelled — feeding the bogus "I paused
this response" noise of ci#642/#643.

`detectRepetitionLoop` (`backend/api/msgmate/repetition_guard.go`) watches a
bounded tail (8 KiB, so the per-delta cost stays constant however long the
response grows) for a unit of at most 240 bytes repeated at least 12 times in a
row over at least 300 characters. The thresholds are deliberately conservative:
a bulleted list, a table, a code fence, a short ASCII rule or an answer that
merely repeats a phrase a few times is never mistaken for a loop.

When the guard trips, `processStreamingResponseReader` stops reading the
provider stream, truncates the repeated run and appends the one-line note
`[stopped: repeated output detected]`, then finishes the turn normally through
the ordinary streaming path (so the truncated snapshot is what gets persisted).
Text emitted before the loop is kept. The round result carries
`repetitionLoop`, and the provider request record reports
`failure_reason=repetition_loop` so the degeneracy is visible in usage
tracking/metrics without parsing the assistant text.

### Context overflow: tool outputs are budgeted (ci#680)

A native agent used to fail mid-task with `exceeded maximum context` after a few
exploratory tool calls. Tool results were appended to the conversation verbatim,
and a single `coder_list` of the monorepo returns ~380 entries while a whole-file
`coder_read` or a `coder_bash` dump is tens of KB, so a handful of them blew the
provider window and the turn died instead of continuing. Three guards now keep a
turn inside the window (`backend/api/msgmate/context_budget.go`):

1. **Per-tool output budget.** Every result is hard-truncated at its own limit
   before it enters the transcript, with an explicit marker that names the tool,
   reports the original size and tells the model how to fetch the missing part
   (`coder_read`: `offset`/`limit`, `coder_list`: `depth`/entry cap, `coder_grep`:
   `include`/`paths`, or redirect to a file and read ranges). The compiled-in
   per-tool budgets are tight for the tools that are cheap to re-issue and
   expensive in context (`coder_list`/`coder_glob` 4 KB, `coder_grep` 6 KB,
   `coder_bash`/`coder_git` 8 KB, `coder_read` the 12 KB default). They are
   configurable per chat/bot with the shared-config key `tool_result_max_chars`
   (either an integer fallback or a `{tool: limit, default: limit}` object) and
   deployment-wide with `OCI_MSGMATE_CODER_TOOL_RESULT_MAX_CHARS`, which clamps
   every per-tool limit.
2. **Conversation budget guard.** Before every request the history is measured
   against the chat's character budget (`context_budget_chars`, deployment
   override `OCI_MSGMATE_CODER_CONTEXT_BUDGET_CHARS`, default 120 000 chars ≈ 30k
   tokens). Above it, older tool results and intermediate narration are replaced
   by a short eviction marker — oldest first, so the newest rounds survive longest
   — a huge non-newest user message is truncated, and as a last resort whole old
   messages are dropped. Leading system messages (an injected compaction summary),
   every user message and the newest turn are never touched, and the
   tool-call/tool-result pairing is repaired afterwards so the request stays valid.
   The guard only ever shrinks a history that already exceeds the budget, so it is
   a no-op on the normal path. The existing provider-driven trim-and-retry
   (ci#394 gap 7) still backs it up for a window smaller than the budget assumes.
3. **A usage report.** The last request of every round is measured and recorded:
   characters used vs. the budget, a conservative token estimate, the message and
   tool-result counts, the largest tool result, how many results were truncated,
   evicted or dropped, and the effective per-tool limits. It travels as
   `context_budget` message metadata and is returned by the `coder_context` tool
   (backed by the coding integration's own name when it ships one), so an agent
   can self-throttle before the window is exhausted (ci#654). Above 60 % of the
   budget a one-line self-throttling notice is merged into the leading system
   message (replaced each round, never stacked).

Regression tests: `backend/api/msgmate/context_budget_test.go` (a turn with four
40 KB tool results stays under the budget and completes; the truncation marker
and the eviction markers are explicit; the guard is idempotent and keeps the
pairing), `backend/api/msgmate/coder_context_tool_test.go` and
`backend/api/msgmate/tools/coder_context_tool_test.go`.

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
