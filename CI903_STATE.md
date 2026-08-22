# ci#903 — r75 consolidation tracker: delivery state (2026-10-27)

## Verdict: already delivered via #1193
- PR #1193 `chore/consolidate-2026-10-27-r75` (dev → main consolidation,
  VERSION 0.0.788) **merged into `main`** on 2026-10-27 (merge `130b11d`,
  parent tip `3c61e9b`).
- `origin/main` (tip `130b11d`) now contains everything the tracker carried:
  - r74 absorbed batch (#1190): #1186 OAuth redirect_uri adjacency, #1187 OAuth
    token-exchange hardening (+ the one-line `stored.Scope` → `stored.Scopes`
    compile fix on the r75 head), #1191 bot JSON-error envelope (ci#865), #1189
    interaction-tool-init envelope dedupe, #1188 REST API delivery-mode docs,
    VERSION 0.0.787
  - #1192: `CI900_STATE.md` delivery-state record (OAuth authorization server
    itself was already merged via #1181 as `3c61e9b`)
  - VERSION 0.0.788 (r75) in `backend/api/metrics/handler.go`
  - No lock refresh needed: `integrations.lock.json` (go_client `79b39a1`,
    mcp `03adb21`, rest_api_tool `a79d3ad`) and all public submodule gitlinks
    were already at their upstream `main` tips.

## Follow-up carried here (VERSION 0.0.789)
- Version bump 0.0.788 → 0.0.789 so the next consolidation has its own
  version const ahead of an imminent promotion/release.
- Nothing else queued: PR CI on the r75 head was green
  (`Analyze`, all four `Run PR CI Go Tests` shards, `Build Backend Image Once`,
  `PR Test Merge Requirement`) before merge.

## Still pending (not part of this consolidation)
- OAuth end-to-end `opencode mcp auth` against ci.msgmate.io + consent UX
  decision (ci#900) — post-deploy verification.
- Production promotion main → production branch (incl. #1185 conflict-resolver)
  — blocked on the deploy-approval lane in PR CI.
- Live Runtime Canary verification of the ci.msgmate.io deployment — blocked on
  ci#898 (`repository_dispatch 'live-canary'` accepted but creates no run).
