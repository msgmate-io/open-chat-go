# ci#900 — MCP OAuth authorization server: delivery state (2026-10-27)

## Verdict: already delivered
- PR #1181 `feat/ci895-900-mcp-oauth` ([Deploy] MCP OAuth discovery + authorization server)
  **merged into `main`** on 2026-10-27 (00:35:51Z), closing ci#895 and ci#900's discovery half
  and implementing the full authorize + PKCE code exchange + register surface.
- `origin/main` (tip `3c61e9b` = merge of #1181) now contains:
  - `backend/api/user/OAuthServer.go` — 5 `UserHandler` methods (authorize / token / register /
    well-known discovery endpoints)
  - `backend/api/user/oauth_server_test.go` — 7 test funcs (PKCE happy path, single-use code,
    expiry, invalid_grant, redirect_uri pinning, off-allowlist rejection)
  - `backend/database/access_token.go` — `AudienceMCPOAuth` const + `MintScopedAccessToken` reuse
  - `backend/server/browser_tokens.go` — allowlist `case` for the MCP serve route (default stays deny)
  - `backend/server/routing.go` — 5 wiring entries (`.well-known/oauth-protected-resource`,
    `.well-known/oauth-authorization-server`, `/register`, `/authorize`, `/token`)

## Follow-ups already in flight (absorbed into r74 umbrella #1190)
- #1186 `fix(oauth): keep redirect_uri validation adjacent to http.Redirect` (CodeQL open-redirect 31/32)
- #1187 `fix(oauth): harden token exchange` (mandatory redirect_uri, registered client_id, scope assertion;
  PKCE S256 enforced per RFC 6749 4.1.3 / 5.1)
- #1190 `chore(consolidate): r74` — merges #1186+#1187+#1191+#1189+#1188 + VERSION 0.0.787.
  PR CI: Analyze SUCCESS, Run PR CI Tests and Deployments SUCCESS, Build Backend Image Once SUCCESS.
  No reviews requested/threads yet (auto-merge lane).

## What ci#900's own issue asked for that is NOT yet closed
- Consent UX (maintainer decision) and the end-to-end `opencode mcp auth` run against
  `ci.msgmate.io` post-deploy — both explicitly deferred to after the PR lands.

## Nothing to implement here
This sandbox started on `ab121dd` (r73, pre-#1181). The task is already merged upstream;
re-creating it would duplicate #1181 and conflict with #1190.
