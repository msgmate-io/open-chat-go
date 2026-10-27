# ci#907 — r76 consolidation tracker: delivery state (2026-10-27)

## Verdict: already delivered via #1197
- PR #1197 `chore/consolidate-2026-10-27-r76` (chore/consolidate-2026-10-27-r76 →
  main, labels `deploy` + `sandbox-test`, no reviews requested — auto-merge
  lane) **merged into `main`** on 2026-10-27 (merge `e2b7e5f` = merge of #1197;
  merged sha `e4d5bb27`, merge time 2026-10-27T11:50:39Z, no pending/failing
  required checks at merge).
- r76's single commit `e4d5bb27` (`chore(version): bump backend VERSION to
  0.0.789 (r76)`) is a **one-line version const bump only — no other code
  deltas**:
  `var VERSION = "0.0.788"` → `var VERSION = "0.0.789"` in
  `backend/api/metrics/handler.go` (verified via the GitHub commits API; the
  merge diff vs the r75 tip `130b11d` is exactly that file, +1/−1).
- `origin/main` (tip `e2b7e5f`) therefore contains everything the tracker
  carried:
  - Absorbed/closed leaf PRs (verified content on main):
    - #1186, #1187, #1191, #1189, #1188 → absorbed via r74 (#1190, merged
      2026-10-27T04:13:12Z), closed with absorbed comments.
    - #1192 → absorbed via r75 (#1193, merged 2026-10-27T04:13:10Z), closed
      with absorbed comment.
    - #1194 stays **open** (dev-track MCP OAuth feature branch, base `dev`):
      main-facing artifacts already on main via #1181+#1186+#1187; only a
      comment-wording delta in `backend/server/routing.go` remains on the
      branch.
  - VERSION 0.0.789 (r76) in `backend/api/metrics/handler.go`.
  - No lock refresh needed: `integrations.lock.json` (go_client `79b39a1`,
    mcp `03adb21`, rest_api_tool `a79d3ad`) and all five public submodule
    gitlinks are already at their upstream `main` tips (verified via
    `git ls-remote` in earlier rounds).

## Deployment verification (merged-deploy-main → ci.msgmate.io)
- `/api/version` returns `0.0.789` (r76 build live); `/` returns 200;
  `/healthz` returns 404 (consistent pre- and post-r76; the liveness path is
  `/api/version`, so this is **not** a regression).
- MCP OAuth surface reachable: `/.well-known/oauth-protected-resource` and
  `/.well-known/oauth-authorization-server` both respond on ci.msgmate.io.
- Live Runtime Canary re-dispatched via `repository_dispatch` type
  `live-canary` (expected_version 0.0.789) at 2026-10-27T10:50Z, run
  open-chat-go-ci#40491518358 — outcome tracked on ci#907.

## Tickets opened this round
- ci#906 (r76 follow-up): live-runtime-canary failing baseline — the canary
  interaction never reaches `finished` (empty bot reply, `state=idle`,
  `error=false`, no canary marker; watcher times out after 420s). Pre-dates
  r76, so r76 did not regress it, but it blocks the post-merge canary gate.
  The merged-deploy `deploy-approval-lane` naming residual is folded in
  (old `no-deploy-approval` warning must be renamed; fresh live-canary
  dispatch after-AI is the fix). Fixes ride the next consolidation PR via the
  private CI repo.

## Tickets closed this round
- None (nothing resolved this round).

## Still in flight / out of scope
- Production promotion (open-chat-go#1195/#1185) — left to the production
  flow; deploy-approval issue open-chat-go#1196 untouched (approver lane).
- #1194 dev-track MCP OAuth branch — stays open.
- ci#906 live-canary fix — rides the next consolidation PR via this CI repo.