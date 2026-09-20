# ci#895 — MCP endpoint OAuth discovery: delivery state (2026-10-31, closed)

## Verdict: ALREADY DELIVERED upstream — no new PR to open

Ticket ci#895 ("MCP endpoint has no OAuth discovery: `opencode mcp auth` 404s
against ci.msgmate.io") is fully implemented and merged upstream. Re-creating it
would duplicate the merged work and conflict with subsequent consolidation
rounds.

## Evidence (verified in this sandbox, 2026-10-31)

- PR **#1181** `feat/ci895-900-mcp-oauth` — "[Deploy] MCP OAuth discovery +
  authorization server (ci#895, ci#900)" — **state: MERGED** to `main` on
  2026-10-27 00:35:51Z (merge commit `3c61e9b9`, feature head `e827ba4b`).
  It carries the code-change labels including the `test` label and the heavy
  deploy suite, as the ci#895 ticket's own acceptance criteria required.
- The feature commits are ancestors of `origin/dev` (verified via
  `git merge-base --is-ancestor 3c61e9b9 origin/dev` → YES), so opening another
  PR against `dev` would just duplicate `e827ba4b` + `efcd4d37` + `172ca877`.
- No open PR in this repo matches 895/oauth/1181 (`gh pr list`), so there is
  nothing to retarget or relabel either.

## What the merged code delivers (acceptance criteria map)

1. `WWW-Authenticate: Bearer resource_metadata=".../.well-known/oauth-protected-resource"`
   challenge on the MCP serve endpoint:
   - `backend/server/browser_tokens.go` (allowlist rows incl.
     `POST /integrations/msgmate_coder/mcp/serve/{endpoint_uuid}` + `mcpOAuthTokenRoutes`)
   - `backend/api/user/OAuthServer.go` — `ProtectedResourceMetadata` / `AuthorizationServerMetadata`
2. RFC 9728 protected-resource metadata (JSON, `authorization_servers`) —
   `OAuthServer.go:133`.
3. RFC 8414 authorization-server metadata + `/authorize`, `/token`, `/register`
   (dynamic client registration) — `OAuthServer.go:209/342/460`, wired in
   `backend/server/routing.go:684-694`.
4. MCP-scoped tokens: `backend/database/access_token.go` —
   `AudienceMCPOAuth = "mcp-oauth"`, accepted via the existing token hash check;
   static bearer / `X-API-Key` / `?token=` path untouched (no regression).
5. Discovery-shape tests: `backend/api/user/oauth_server_test.go` — 8 test
   functions incl. `TestOAuthDiscoveryDocumentsShape`, PKCE code flow,
   redirect_uri pinning, off-allowlist rejection, token-exchange hardening
   (RFC 6749 4.1.3/5.1 from follow-ups `172ca877`, `efcd4d37`).

## Why no new PR here

- The ticket's criteria 1 & 2 (`opencode mcp auth` handshake live on
  ci.msgmate.io + token accepted by the serve endpoint) are **deployment
  verification**, performed post-deploy on the merged release, not a code
  change. ci#900 (the authorization-server twin) was the round that carried
  the deploy; its tracker (CI900_STATE.md) defers the live handshake run to
  after the PR landed — also not a code path to open here.
- The maintainer/consolidation loop has already promoted the OAuth server to
  `main` and `dev`; any further PR would be a no-op duplicate conflicting with
  the r75-r79 consolidation chain.

## Notes

- This sandbox restarted on `main` (tip `3faca383` [Dev promotion] #1271) with
  `origin/dev` at `29f16e75`; the branch tip is a checkout of origin's main,
  so the OAuth files are present in the working tree.
- No files were changed and nothing was pushed for this ticket; this state file
  is the durable record of the verdict.
