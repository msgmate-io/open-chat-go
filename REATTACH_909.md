# ci#909 re-attach marker — sandbox state

## Investigation so far (this sandbox, CI ticket analysis)

- Ticket #909 lives in `msgmate-io/open-chat-go-ci` and is the CI loop
  self-verification tracking issue for a **`plan → repl → review → consolidate`
  native-coder run (108 chats)**.
- The NATIVE ROOT CAUSE is **backend provider admission saturation**:
  `ProviderSaturationError{ Key, MaxConcurrency/MaxConcurrency=…, InFlight,
  QueueWaiting, Waited }`. Distinct type in
  `backend/api/msgmate/provider_admission.go` — created on `acquire()` queue
  timeout (line ~596), distinct from provider errors on purpose, so "back
  pressure" vs "provider failure" can be told apart.
- The saturation error message already carries *which bound* decided the cap:
  `LimitingSource()` → `env:OCI_…`, `agent:KEY`, `pool:KEY` (ci#602) — so the
  deploy lever is already the right pointer.
- The stale-slot watchdog (ci#799) already reclaims leaked slots:
  `watchContext` + `sweepStaleSlots` + `releaseSlot(…, reason)`.
- **Working hypothesis for the remaining CI failure**: saturation is being
  *treated as a provider failure* and being *retried with provider backoff*,
  instead of being surfaced immediately as back-pressure with a *clear
  saturation signal* (no meaningful semantic in the "plan" phase; the 108-chat
  run's failure rate is exactly what back-pressure produces, and the CI went
  red on "the same chat modes still fail").
- Decision: **FOCUSED backend change** — when provider admission
  saturation happens on the *pre-stream* (non-streaming) attempt to reach the
  provider key, do NOT let the provider retry loop absorb it (which makes the
  saturation error indistinguishable from a provider failure and keeps the
  chat in the exact mode where it failed). Instead surface it immediately as
  retryable provider back-pressure so CI's per-chat failure mode stays honest
  ("provider_402_429_loop" etc), and the evaluation distinguishes
  true provider errors from actual saturation.

## Files examined (already read) — no changes are lost

- `backend/api/msgmate/provider_admission.go` (limiter, ProviderSaturationError,
  ResolveProviderAdmissionConfig, env levers)
- `backend/api/msgmate/provider_retry.go` (retry loop, `StatusPay…` 402/429)
- `backend/api/msgmate/openai.go` (admission → request flow) — partial
- `backend/api/msgmate/provider_retry_test.go` — partial
- CI run 40856243381 (failing), its "Report and guard" job logs
- CI runs 40519576936 / 40667838696 (baseline) — Report-and-guard job logs

## Open state (@REATTACH-909@HIGH-WATER)

Read of `openai.go` around the admission call and the retry-saturation
interaction was in progress when the previous run stopped. The retry loop in
`provider_retry.go` retries 402/429 HTTP statuses with backoff and counts
"ProviderAttempts"; the admission saturation error is NOT an HTTP status so it
is *not* currently matched by `isRetryableProviderRequestError` — confirm this
precisely and, if needed, make `ProviderSaturationError` a distinct
non-HTT P provider error surfaced immediately (do not huddle it with
502/503/504).

Next concrete step: read `backend/api/msgmate/openai.go` where the
`providerAdmissionLimiterFor(key, policy).acquire(...)` call site is wired into
the provider request path (was `sed -n '690,740p'` / `sed -n '1138,1252p'` /
`sed -n '1252,1306p'` earlier), then make the minimal change + unit test in
`backend/api/msgmate/provider_retry_test.go`.