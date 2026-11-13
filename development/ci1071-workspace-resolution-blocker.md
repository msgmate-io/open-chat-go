# ci#1071 — canary: `coder_list` cannot resolve the chat workspace

Canary evidence (ci.msgmate.io, run 43491886141, `main@d058241a`): auth and
interaction creation succeed, then the bot's first tool call dies with

    coder_list -> coder tool api failed (400): workspace "open-chat-go" not found for this chat.

The canary marker is never emitted because the very first read-only tool call
fails. This document records the proven root cause and where the fix has to land.

## The failing lookup is NOT in this repository

The 400 is raised by the coder integration module
(`msgmate-io/open-chat-go-msgmate-coder`), which is composed into core at
`clients/integrations/msgmate_coder_integration` (see
`.github/workflows/integration-module-ci.yaml` and `DEVELOPMENT.md:183`). Nothing
in this tree can produce that message:

- `git grep 'not found for this chat'` / `resolveSelectableWorkspace` on
  `origin/dev` hits nothing outside this investigation note.
- `git grep 'coder_workspace'` on `origin/dev` hits nothing at all: core seeds
  `tool_init` with `git_repo`/`auto_sandbox` only (see
  `backend/agentdefaults/taskrepo.go:44,233`) and never stamps a workspace
  *identity*.
- The only coder files here are the host-side tools
  (`backend/api/msgmate/tools/coder_context_tool.go`, `coder_compact_tool.go`,
  `coder_schedule_tool.go`).

## Root cause (proven from the integration source, candidate #1)

In `open-chat-go-msgmate-coder@main`, `workspace.go:716-735`
(`resolveCoderContextOpts`) takes the configured default hint:

```go
hint := coderConfigString(config, coderConfigWorkspaceUUIDKey)   // coder_workspace_uuid
if hint == "" {
    hint = coderConfigString(config, coderConfigWorkspaceKey)    // coder_workspace  -> "open-chat-go"
}
if hint != "" {
    workspace, resolveErr := resolveSelectableWorkspace(db, chat, hint)
    if resolveErr != nil {
        return nil, resolveErr          // <-- hard 400, no fallback
    }
    workspaceUUID = workspace.UUID
}
```

and `tools_api.go:143-168` resolves that hint strictly **owner-scoped**:

```go
func resolveSelectableWorkspace(db *gorm.DB, chat models.Chat, hint string) (CoderWorkspace, error) {
    hint = strings.TrimSpace(hint)
    ...
    // uuid, then name, both restricted to:
    ... AND owner_user_id IN ?", hint, []uint{chat.User1Id, chat.User2Id}
    ...
    if err == gorm.ErrRecordNotFound {
        return CoderWorkspace{}, fmt.Errorf("workspace %q not found for this chat", hint) // tools_api.go:163
    }
```

So **the binding is configured** (hint = `"open-chat-go"`, the *name*), and the
read path resolves it against `owner_user_id IN (chat.User1Id, chat.User2Id)` and
misses — the workspace row is owned by a bot/service account (or admin-only
kaniko scope) that is not a chat participant. Candidate #2 is ruled out: `tool_init`
binding is not the gap on this path, the resolution is.

Compounding it: the ci#526 auto-provision fallback right below
(`workspace.go:736-751`) is guarded by `if workspaceUUID == ""`, so a *failed*
hint resolution returns the 400 **before** the fallback is ever considered. A
stale or mis-owned configured default therefore dead-ends every tool — exactly
the ci#399/ci#429 "hard-fail instead of resolving/provisioning" class.

## Fix (must land in `open-chat-go-msgmate-coder`)

1. `resolveSelectableWorkspace`: keep the participant-scoped lookup first (that
   is the authorization boundary for a *user-supplied* selection), then, for the
   **configured default** path only, add an owner-relaxed fallback lookup by
   UUID then by name (still `deleted_at IS NULL`). Names are unique per
   kaniko/provisioning scope, so this resolves `"open-chat-go"` to the row the
   chat is actually meant to use.
2. `resolveCoderContextOpts` (`workspace.go:728-734`): treat an unresolvable
   **configured** hint as "no binding" (log it) and fall through to the existing
   ci#526 per-chat auto-provision fallback instead of returning a 400. An
   unresolvable *explicit user* selection (`handleSelectWorkspace`,
   `api_handlers.go:337`) must keep returning the error, so a typo is still
   reported.
3. Regression tests (extend the sqlite harness in
   `workspace_resolve_test.go` — `newCoderTestDB`, `newCoderTestChat`):
   - a chat whose `coder_workspace` names a workspace owned by a *non*-participant
     must resolve to that workspace UUID (fails on current `main`: 400);
   - a chat with **no** binding and no configured default must auto-provision its
     per-chat sandbox and must not surface
     `workspace %q not found for this chat` from a read-only `coder_list`;
   - an explicit unresolvable selection still errors (guard against over-relaxing).

## Why no PR was opened against this repo

The change has no core-side component: the hint is *read* by the integration, not
written by core (`coder_workspace` does not exist on `origin/dev`). A PR to
`open-chat-go` `dev` for this fix would be empty or unrelated, so it was not
fabricated. This branch is a docs-only record of the blocker and the patch spec;
the fix belongs in `open-chat-go-msgmate-coder` (its only branch is `main`; the
`dev`-trunk + `Closes`/label process described in this repo's `DEVELOPMENT.md`
does not apply there verbatim).
