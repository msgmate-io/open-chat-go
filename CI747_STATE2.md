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

1. No compile/test evidence yet that the tree actually builds and the targeted
   unit tests pass with all of the above wired (local fast checks only). Run
   gofmt + go test on: backend/cmd/..., backend/api/msgmate/...,
   backend/api/bots/..., backend/coordination/..., backend/workqueue/...
2. The umbrella acceptance: PR opened against main with the applicable CI
   labels (this monorepo: targeting main implies full suite per DEVELOPMENT.md —
   label is `test`; see workflow policy before adding more).
3. Report a concise summary: what changed (likely: targeted wiring/regression
   tests still missing + verification), PR URL, pending CI.

## Do NOT

- Do not create new leaf tickets; this is the umbrella (ci#747) and its fixes
  stay on their existing tickets/PRs.
- Do not run heavy local suites (> targeted packages) nor poll CI.
