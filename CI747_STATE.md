# CI747_STATE.md — coordination continuity reliability (ci#747)

Working state for the ci#747 umbrella run. This file is a branch-local tracking
doc: it ships on the ci branch only and is retired from main (the final PR
drops it). It is not a product artifact.

## Snapshot (re-attach, 2026-10-29)

Sandbox: `/workspace/coder-129b52a0-.../open-chat-go` (repo `open-chat-go`,
origin `msgmate-io/open-chat-go`, branch `ci/747-coordination-continuity`).
The previous run (2026-10-28) built the coordination-continuity scaffold onto
the then-current `origin/main` (r78, `5d92ac2`), verified it, and pushed the
branch; the branch was recorded here and re-created fresh on top of the branch
head. On re-attach `origin/main` had moved to r79 merge `89b37edc` (which
contains the r78 base `5d92ac2`), the sandbox was stale, and the branch was
regenerated on top of the new main (`89b37edc`). This re-attach (2026-10-29
evening) fast-forwarded the branch `9a81d246..6a25a83c` (RC4 regression commit
+ Pin Freshness Gate hardening). `git status` is clean; HEAD is `6a25a83c`
(plus the state refresh below), and PR #1231's head ref points at it.

## What this branch carries

- `CI747_STATE.md` — this tracking file (branch-local; retired from main).
- `backend/workqueue/bot_reply_archived_test.go` — RC4 regression tests:
  - `TestEnqueueBotReplyAfterArchive` — after the worker finishes + archives a
    stable-ID task, a follow-up enqueue for the same chat must not 500 /
    `ErrTaskIdConflict` (must stay on the stable base id in the unit layer, and
    fall back to a unique id in production where the archived task persists).
  - `TestBotReplyTaskRetentionArchivedBlocked` — documents the exact asynq
    failure the 0.0.744 hotfix guards against.
- `backend/go.sum` — Pin Freshness Gate hardening (`6a25a83c`): records the
  canonical sumdb checksums for the `github.com/niemeyer/pretty`,
  `modernc.org/ccgo/v3` and `modernc.org/gc/v2` `/go.mod` files (verified
  byte-for-byte against `sum.golang.org/lookup`, inserted at their sorted
  positions) so the frozen-workspace build resolves them from go.sum without a
  live sum.golang.org tile fetch — removing the sporadic 5xx that flaked the
  gate on the previous umbrella head.

Method: fast targeted checks (the enqueue-after-archive regression tests +
coordination/queue-tasks/msgmate unit tests + gofmt/vet); PR CI is the gate.

## Verified inventory (present on this branch, references confirmed)

| Area | File(s) | Status |
|---|---|---|
| RC1 self-scheduler wiring | `SelfMessageScheduler.go`, `SchedulerCron.go`, `SelfMessageRuntime.go` | present, referenced by `server.go` (single non-fatal gate) |
| RC1 boot test | `backend/cmd/self_message_wiring_test.go` | present |
| RC2/RC3 cap/transient continuation | `turn_cap_continuation.go`, `turn_continuation_dispatch.go`, `CoderSchedule.go`, `sandbox_capacity.go` | present; `MsgmateBotAIHandler.go` dispatches cap continuation on transient infra, metadata `ci747_continuation_attempts=1` |
| RC4 archive-enqueue | `bot_reply_archived_test.go` | present on this branch |
| RC6 shared coordination instructions | CI repo AGENTS.md / config-delta (QW1) | separate PR (ci#748) |
| RC7 dedupe | `backend/coordination/ownerkey.go` + tests | present on this branch |

## Deliverable (this branch's PR)

- [x] Branch `ci/747-coordination-continuity` pushed; PR #1231 open against
      `main` with the `test` label.
- [x] PR #1231 is non-draft, carries the RC4 regression tests, and its body
      references the leaf it closes (RC4 = the archive/enqueue slice of ci#747,
      live canary landed in ci#768 MERGED) via `Closes msgmate-io/open-chat-go-ci#918`.
- [x] Local checks confirmed (re-attach run 2026-10-29, HEAD `ee8bd4f`):
      `TestEnqueueBotReplyAfterArchive` + `TestBotReplyTaskRetentionArchivedBlocked`
      PASS; coordination / queue/tasks / api/msgmate + tools unit suites green;
      gofmt clean; go vet clean.
- [x] Local check results reported on the interaction (fast, targeted only;
      PR CI is the gate).
- [x] Pin Freshness Gate hardening (`backend/go.sum`, `6a25a83c`) fast-forwarded
      into the branch and pushed; PR #1231 head is `6a25a83c`. On the new head
      (single reads): CodeQL success, Go test shards 1–4/4 success; the gate
      run for `6a25a83c` itself is in progress — PR CI is the gate.
