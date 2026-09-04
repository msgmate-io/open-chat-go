# ci#916 — r79 consolidation tracker: delivery state (2026-10-29, in progress)

Consolidation round r79, assembling currently-open public PRs and the ready
private-side changes into **one** tested consolidation PR
(`chore/consolidate-2026-10-29-r79` → `main`).

## In scope (merged onto the r79 head)

- `ci-514-github-run-bridge` — merge of ready PR #1223 (typed `github_run`
  list/view nativecoder bridge, bash-retirement phase 1) **and** PR #1219
  (mirrored-upstream `chore/ci514` branch with the `gofmt` final-newline
  follow-up `4d6e54cc`). Branch tip `4d6e54cc`.
- `ci915-coder-sandbox-regression-bugfix` — merge of ready PR #1215: ci#915
  exempts single-sender progress from the consecutive-automated loop breaker.
- `ci/865-shared-chat-scoping` — merge of ready PR #1222: ci#865 scopes
  chat publish/unpublish to the acting user and returns JSON errors.
- `chore/ci903-close-out` — merge of ready PR #1221: retires the r75
  consolidation tracker (`CI903_STATE.md` now a true leaf, historical record).
- `chore(version)`: backend VERSION 0.0.795 → 0.0.797 (r79).
- Public pin refresh: NOT needed. All public pins verified fresh against
  upstream `main` tips: `integrations.lock.json` (go_client `79b39a1`, mcp
  `03adb21`, rest_api_tool `a79d3ad`) and every public submodule gitlink
  (python `bde880a`, tool-interface `b85ed94`, integration-interface `ada7c35`,
  js `ff7c0db`, build-tools `ab2b171`) match their upstream heads.

## Private-side (this round)

- **open-chat-go-helm-chart**: PR #6 `chore/ci-concurrency-caps`
  (ASYNQ_CONCURRENCY=8, coder provider max concurrency=5) — open, mergeable,
  no checks required; merged into the private Helm chart `main` this round
  (persists the ci.msgmate.io caps across `helm upgrade`).
- **open-chat-go-ci**: nothing ready yet this round: #914 (ci#898
  log-monitoring canary runner) is in review; #763 (ci#758 link-rules proofs)
  is still draft.
- **Mobile / llm-coding-context**: no open PRs.

## Status

- [x] r79 head assembled from the 4 ready public branches, rebased onto the
      r76-r78 main chain (+#1221, #1222, #1223 re-applied), one historical
      conflict (`CI903_STATE.md`) resolved
- [x] VERSION bumped 0.0.795 → 0.0.797 (r79)
- [x] helm chart #6 merged into the private chart repo
- [x] consolidation PR #1224 `chore/consolidate-2026-10-29-r79` opened
      (`deploy` + `sandbox-test` labels)
- [x] tracker issue ci#916 created (private `open-chat-go-ci`, labelled
      `consolidation`)
- [ ] PR #1224 checks green (Go tests, E2E, sandbox, Pin Freshness Gate)
- [ ] PR merged to `main` (merge when green)
- [ ] merged-deploy-main deployment on ci.msgmate.io verified (rollout +
      `/api/version` 0.0.797)
- [ ] live canary / selfcheck ok
- [ ] tracker closed (ci#916)
