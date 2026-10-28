# ci#903 — r75 consolidation tracker: delivery state (2026-10-27, closed)

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

## Follow-ups carried here
- **#1200** `chore/consolidate-2026-10-27-r75-followup` (merged into `main` on
  2026-10-27 as merge `2dbbd40`): VERSION bump 0.0.788 → 0.0.789 so the next
  consolidation has its own version const, plus this `CI903_STATE.md` record
  (at that point, added by commit `6192193` and re-verified byte-identical on
  current `main`).
- Later r76–r78 consolidations landed on `main` independently (#1197, #1206,
  #1211, #1218); the tracker only accounts for exactly the r75 scope above.
- PR CI on the r75 head was green before merge (`Analyze`, all four
  `Run PR CI Go Tests` shards, `Build Backend Image Once`,
  `PR Test Merge Requirement`).
- No pin refresh was needed any round: `integrations.lock.json`
  (go_client `79b39a1`, mcp `03adb21`, rest_api_tool `a79d3ad`) and all public
  submodule gitlinks were already at their upstream `main` tips.

## Still pending (not part of this consolidation)
- OAuth end-to-end `opencode mcp auth` against ci.msgmate.io + consent UX
  decision (ci#900) — post-deploy verification.
- Production promotion main → production branch (incl. #1185 conflict-resolver)
  — blocked on the deploy-approval lane in PR CI.
- Live Runtime Canary verification of the ci.msgmate.io deployment — blocked on
  ci#898 (`repository_dispatch 'live-canary'` accepted but creates no run).
