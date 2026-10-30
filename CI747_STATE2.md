# ci#747 umbrella drive — final record

Drive: coordination-continuity reliability umbrella (self-resume/lock, error
recovery, identity/permissions, dedupe). PR for this record: #1254
(`devtracker-747-coordination-continuity` → main, label `test`).

## Outcome vs acceptance

- Lead/workers recover from a stalled/aborted turn without a human re-dispatch:
  covered by the self-message scheduler (RC1) + continuation dispatch on
  cap-exhaustion (RC2) + bounded provider retry (RC3) — all merged on main via
  PR #1231 (`6b89916`).
- Lead's own entry is declaratively scheduled (RC1): `SelfMessageScheduler` is
  boot-wired; `SetEnqueue`/`SetCancel` use the real dispatch/cancel path; a
  newer schedule replaces (never duplicates) the previous task via the stable
  task id; focused tests present.
- Lead can list/re-dispatch its work and de-duplicate an owned ticket
  (RC7/QW4): dispatcher owner-key dedupe is (ticket, lane), one owner per
  ticket, asserted in tests; RC5 identity gap documented in the umbrella (the
  lead's operator-API token is `Forbidden`; the workflow route is the primary
  stop/resume path for chats the lead does not directly own).
- Live canary (QW3): enqueue-after-archive must not 500 (RC4 regression) and a
  scheduled actor must re-fire within one interval — these are the process-side
  live assertions tracked from this umbrella; code-side regression tests are on
  main.

## Fast targeted verification (this sandbox, redis on 127.0.0.1:6380)

- `go test -count=1 ./api/msgmate/... ./coordination/ ./workqueue/` → all ok
- `go build ./api/msgmate/... ./coordination/... ./workqueue/... ./queue/...` → OK
- `go vet ./api/msgmate/... ./coordination/ ./workqueue/` → OK
- `gofmt -l` on api/msgmate, coordination, workqueue, cmd → clean
- No full-suite/E2E/Docker or canary runs locally; PR CI (label `test` on
  #1254) is the authoritative full-suite gate and the live canary runs out of
  this repo's deployment process, tracked separately under the umbrella.

---

# ci#747 drive state snapshot

Goal: record what is already merged on main vs what this drive must implement,
without re-running broad listings that burn context.

## Already merged at HEAD (6b89916, PR #1231, "ci#747: RC4 enqueue-after-archive
regression + RC1 self-scheduler wiring assertions")

- RC1 self-message scheduler runtime + parser + scheduler + tests shipped under
  `backend/api/msgmate/` (SelfMessageRuntime.go, SelfMessageScheduler.go,
  SchedulerCron.go, SelfMessageGuard.go, SelfMessageCoordination.go ...).
- Server boot arms the runtime exactly once, non-fatally, inside the
  run-scheduler gate: backend/cmd/server.go at ~line 1258.
- Wiring guards: backend/cmd/self_message_wiring_test.go (whole-file read).
- RC2/RC3 continuation decision + dispatch seam:
  backend/api/msgmate/turn_cap_continuation.go, turn_continuation_dispatch.go.
- RC4 bot-reply enqueue-after-archive regression: backend/workqueue/bot_reply_archived_test.go.
- RC7 dedupe key: backend/coordination/ownerkey.go + AgentDispatchOwnerKey.go +
  AgentDispatchDedupe_test.go.

## Notes recorded while reading

- backend/api/msgmate/SelfMessageRuntime.go: StartSelfMessageRuntime resolves
  SelfMessageSchedulesFromEnv, returns (nil,nil) when nothing configured; the
  runtime handles bot resolution errors degradedly (logs schedule, keeps
  reconciling) — good.
- backend/cmd/server.go ~1242-1268 wires StartSelfMessageRuntime inside
  run-scheduler gate; does not fail boot on error.
- backend/cmd/self_message_wiring_test.go guards: boot files must contain
  msgmate.StartSelfMessageRuntime( and SelfMessageScheduleEnvVar, must NOT abort
  boot on error, and server.go must call it exactly once.
- turn_cap_continuation.go: pure decision; cap outcomes + transient infra map to
  a single continuation prompt; max 1 attempt per chain; correlation id stable.
- turn_continuation_dispatch.go: dispatch seam over coder_schedule runner
  (ScheduleCoderTurn(chatUUID, prompt, delaySeconds, correlationID)), 45s delay
  when unspecified, visible metadata keys incl. ci747_continuation_reason.
- AgentDispatchOwnerKey.go: dedupe via explicit DedupeKey, else (ticket, lane)
  owner key when an explicit lane tag is present; force:true is the recovery.

## Open gaps for THIS drive (ci#747 umbrella, no new leaf tickets)

1. VERIFIED (2026-10-30 rerun): gofmt clean on api/msgmate, coordination,
   workqueue, cmd; `go test` green on backend/cmd, api/msgmate(+tools),
   coordination, workqueue, and — once a local redis was provisioned
   (ASYNQ_REDIS_ADDR=127.0.0.1:6379 on an alpine sandbox without bundled
   redis/docker) — backend/api/bots passed 18.5s. The earlier api/bots
   failures were infra (missing redis), not code regressions.
2. The umbrella acceptance: PR opened against main with the applicable CI
   labels (this monorepo: targeting main implies full suite per DEVELOPMENT.md —
   label is `test`; see workflow policy before adding more).
3. Report a concise summary: what changed (likely: targeted wiring/regression
   tests still missing + verification), PR URL, pending CI.

## Verification evidence (2026-10-30 rerun, after rebasing onto current main e2decab8)

- Branch: devtracker-747-coordination-continuity -> PR #1254, force-pushed
  d918846a (docs-only, two commits: umbrella drive record + finalized acceptance
  mapping/verification). Blob CI747_STATE2.md identical to the prior 9bdb7da6
  push.
- gofmt: no files listed under api/msgmate, coordination, workqueue, cmd.
- go test: backend/cmd ok 7.878s; api/msgmate ok 2.299s (+/tools 0.080s);
  coordination ok 0.004s; workqueue ok 0.088s; api/bots ok 18.529s with
  ASYNQ_REDIS_ADDR=127.0.0.1:6379.
- go vet clean on the same packages.
- NOTE: api/bots tests need a live asynq/redis at `redis:6379` (or
  ASYNQ_REDIS_ADDR) — CI provides it via docker compose (workflow lines
  1076/1266/1421); a bare alpine sandbox must run redis-server locally first.

## Do NOT

- Do not create new leaf tickets; this is the umbrella (ci#747) and its fixes
  stay on their existing tickets/PRs.
- Do not run heavy local suites (> targeted packages) nor poll CI.
