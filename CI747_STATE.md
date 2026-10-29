# CI747_STATE.md — coordination continuity reliability (ci#747)

Working state for the ci#747 umbrella run. This file is untracked and lives in
the sandbox only; it never ships.

## Snapshot (re-attach, 2026-10-28 morning)

Sandbox: `/workspace/coder-129b52a0-.../open-chat-go` (repo `open-chat-go`,
origin `msgmate-io/open-chat-go`, branch `main` at `5d92ac2`).
`git status` is clean, so the previous run's in-progress changes were either
committed+merged or lost; **all listed files already exist and compile**, so the
run-as-merged policy holds: verify inventory+unit tests, then push the branch.

## Verified inventory (files present, `git status` clean, HEAD 5d92ac2)

| Area | File(s) | Status |
|---|---|---|
| RC1 self-scheduler wiring | `backend/api/msgmate/SelfMessageScheduler.go`, `SchedulerCron.go`, `SelfMessageRuntime.go` | present, referenced by `server.go:1258` + `worker.go:150` (single non-fatal gate) |
| RC1 boot test | `backend/cmd/self_message_wiring_test.go` | present (TestServerBootArmsSelfMessageSchedules) |
| RC2/RC3 | `backend/api/msgmate/turn_cap_continuation.go`, `turn_continuation_dispatch.go`, `CoderSchedule.go`, `sandbox_capacity.go` | present; `MsgmateBotAIHandler.go:1424-1436` dispatches cap continuation on transient infra (`TransientFailures`/`SandboxCapacityFailures`), metadata `ci747_continuation_attempts=1`, stable correlation task id |
| RC3 provider retry | provider-retry options wired in `MsgmateBotAIHandler.go:430-433, 540` (`providerRetryOptionsFromConfig`, `OnRetryAttempt` reporter) | present |
| RC4 archive-enqueue | `backend/workqueue/bot_reply_test.go:32` regression comment (`archived/finished task ... must not ErrTaskIdConflict`) | present |
| RC6 shared coordination instructions | coordination instructions (AGENTS.md / config-delta) in CI repo, opened from ci#747 (QW1) | separate PR; QW2-ci#744 dependency, no new ticket |
| RC7 dedupe | `backend/coordination/ownerkey.go` + `ownerkey_test.go`, `reconcile.go` | present (OwnerKey/TrimPrefix normalisation) |
| RC5 identity/permissions | gap filed separately; worktree leaves it documented | no code |

## What is NOT yet in this tree (candidate gaps)

- Anything else shown by the previous run that is missing from this tree
  (the build/tests are the check).

## Checks run in this session

- (to record here as they run: gofmt -l, go vet, go test ./cmd/..., build)

## Deliverable

- [ ] Push branch `ci747/coordination-continuity-reliability` to origin
  (never to main), open PR against `main` with CI labels (CI), report PR URL.
- [ ] Include the two cap/recovery tests and the wiring tests in the PR.
- [ ] Report local check results (fast, targeted only; PR CI is the gate).
