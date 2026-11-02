### General Development Guide

Development should always use the development compose via `docker compose build && docker compose up` (*likely the development compose is already running!*).
Use the development server backend at `http://localhost:1984` and run commands inside the backend container if needed.

The frontend and backend code automatically reloads in the backend container, so after a short rebuild and reload time you can directly test features yourself.
You should always try to call and test endpoints you implement while you are implementing them and check that they are working.

You should only implement actual integration tests if asked, or suggest to implement them if you really think appropriate.

### Contributing, CI and deployment

Open feature, bugfix, docs and chore pull requests against `dev` with the
ordinary `test` label. The maintainers' release hops (`dev` → `main` →
`production`) carry the private release labels; the full PR/CI/deployment and
consolidation policy, the leaf-issue rules and the release automation are
agent-facing and live in the private msgmate-coder integration docs
(`docs/agent/development-workflow.md`), served to a coder interaction through
`coder_docs`. The public repository keeps the workflow files and the code.

See [`docs/coordination.md`](docs/coordination.md) for the public coordination
summary and lane-ownership runtime contract.

### Code design and implementation notes

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
(see `clients/integrations/community_coder_integration/coder/devsetup.go`, `DevSetupScript`).

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
  working (see `clients/integrations/community_coder_integration/coder/devsetup.go`, `DevSetupScript`).
- The per-repo default is configurable per bot or interaction: a
  `coder_dev_setup` map may carry a `profiles` table next to its `profile`
  default (`profiles: {msgmate-io/open-chat-go: full-development,
  msgmate-io/open-chat-go-mobile: full-android}`). The longest matching
  repository prefix wins and a repository outside the table keeps the declared
  default, so one bot config can serve several repositories with different
  development set-ups instead of forcing every interaction onto the same one.
- `clients/integrations/community_coder_integration/coder/devsetupmatrix.go` is the audit surface of that per-repo
  default: `DevSetupMatrixPlan` resolves what a ci.msgmate.io coding interaction
  on each configured repository actually gets (sandbox palette variant, profile,
  submodules, extra checkouts), `ValidateDevSetupMatrix` lists defects and
  `DevSetupMatrixGaps` names the repositories that still start half-set-up and
  need a `coder_dev_setup` declaration. `RenderDevSetupMatrixMarkdown` renders the
  operator table; the unit tests keep the public profile catalogue, the per-repo
  default table and the sandbox palette from drifting apart — every repository
  the palette maps a ci.msgmate.io trigger to must carry a per-repo entry of its
  own (`clients/integrations/community_coder_integration/coder/devsetup_perrepo_test.go`), so a client, integration
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
  `clients/integrations/community_coder_integration/coder/devsetup.go` (`ResolveDevSetup`, `DevSetupScript`).
- `clients/integrations/community_coder_integration/coder/devsetupfleet.go` is the per-bot / per-use-case audit of
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

The `development-workflow` page holds the PR/CI/deployment, consolidation and
ci2/native-runtime policy. Maintainers who need the runbooks get access to the private integration
repository (or ask a coder agent to quote a page through `coder_docs`). Do not
add agent runbooks, playbooks or an agent-doc tree back to this repository:
`go test ./docguard/` in `backend/` fails when agent-only documentation
reappears publicly.

## Integration-owned agent turns (Host v5 Agent runtime, ci#394)

`integrationinterface.HostVersion` 5 adds `Host.Agent()`, the core's generic tool
loop exposed to compiled-in integrations. It lets a coding integration (the
`msgmate_coder` integration) own a full agent turn — its system prompt, tool set,
tool-init and turn hooks — while the core keeps ownership of provider
selection/streaming, message persistence and websocket frames, the
confirmation/parked-action flow, context budgeting/compaction and the generation
registry. An integration therefore never reimplements the platform.

The `msgmate_coder` integration registers a `ChatBackendFunc` for the
`msgmate_coder` chat backend that calls `Host.Agent().Run(ctx, AgentRequest{...})`.
The core adapter (`backend/integrationhost/agent.go`) bridges that onto
`msgmate.RunAgentTurn` and carries the bot-reply turn on the context, so the same
reply path runs for a coder chat as for any native bot.

`AgentRequest.Hooks` carries the integration-owned turn behaviour: `PreCallInit`
(seed tool-init), `NormalizeToolCall`, `ToolFailureGuidance`, `ToolBudgetExempt`
and `ToolSchemaExtend`. Precedence is **per hook**: the core only applies a hook
when the integration declares it, so a partial hook set never disables the core's
fallback for the hooks it does not own yet — this keeps every intermediate state
green.

Because the decoupling guard (ci#423) forbids an integration importing
`backend/...`, the coder policy now lives in the shared, decoupled public coder
library (`open-chat-go-community-coder-integration/coder`): the turn hooks, the
system-prompt composition, the typed GitHub/git surfaces, the consolidation
planner/runner, the coder compaction/patch/session-trust surfaces and the
config defaults. `backend/nativecoder` is **deleted**; the core imports the
shared library, and the config defaults are applied through the integration
Definition hooks (`backend/agentdefaults`). The two coder integrations
(`msgmate_coder` private, `community_coder` public) consume the same library and
are kept mutually exclusive by the `!community_coder` profile exclusion so they
never register the `coder_*` tools together. The bot-rotation registry moved to
`backend/api/msgmate/rollout.go` (its only consumer).

## Decoupled integration builds (ci#423)

Once an integration's shipped packages no longer import `backend/...` (verified by
`development/scripts/check_integration_decoupling.py`), it can be built against its
minimal compile inputs only:

- pass `decoupled: true` to `.github/workflows/reusable-integration-module-ci.yaml`, or
- set `DECOUPLED=true` when calling `development/scripts/integration_module_ci.sh` directly.

That path skips profile materialization, so the compile inputs are the integration
module plus the pinned `clients/go_integration_interface` module. `decoupled: true` is
a **hard gate**: a decoupled build defaults `REQUIRE_DECOUPLED` to `DECOUPLED`, so the
decoupling guard (`development/scripts/check_integration_decoupling.py`) failing is a
job failure and the module cannot silently re-couple to `backend/...`. It is the
per-integration prebuild precondition from the ci#345 design
(`frontend/pages/docs/content/integration-prebuild-design.mdx`), so it is safe to
enable the moment the integration's decoupling lands; the migration recipe
for the remaining integrations lives in
`frontend/pages/docs/content/integration-development.mdx`.

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
