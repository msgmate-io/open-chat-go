# ci#1071 — canary: `coder_list` cannot resolve the chat workspace

Canary evidence (ci.msgmate.io, run 43491886141, `main@d058241a`): auth and interaction
creation succeed (`state=finished`, `error=false`), then the bot's very first tool call
dies with

    coder_list -> coder tool api failed (400): workspace "open-chat-go" not found for this chat.

The canary marker is never emitted because the first read-only tool call fails.

Status: root cause located in the **coder integration**
(`msgmate-io/open-chat-go-msgmate-coder`), not in open-chat-go core. No patch to
open-chat-go core is justified by the evidence, so this branch carries the diagnosis only
and deliberately opens no PR against `dev`.

## The failing lookup is NOT in this repository

The 400 is raised by the coder integration module, which is composed into core at
`clients/integrations/msgmate_coder_integration` (see
`.github/workflows/integration-module-ci.yaml` and `DEVELOPMENT.md:183`). Nothing in this
tree can produce that message:

- `git grep 'not found for this chat'` / `resolveSelectableWorkspace` on `origin/dev` hits
  nothing outside this investigation note.
- Core only knows the ci#399/ci#429 class:
  `no workspace selected for this chat; call coder_select_workspace first`
  (`backend/api/tools/confirmable_actions_test.go:12`) — a different message on a
  different code path.
- `git grep 'coder_workspace'` on `origin/dev` hits nothing at all: core seeds `tool_init`
  with `git_repo`/`auto_sandbox` only (`backend/agentdefaults/taskrepo.go:37,44,233`) and
  never stamps a workspace *identity*.
- The only coder files here are the host-side tools
  (`backend/api/msgmate/tools/coder_context_tool.go`, `coder_compact_tool.go`,
  `coder_schedule_tool.go`).

The message itself is raised in the integration at `tools_api.go:163`, inside
`resolveCoderContext` -> `resolveSelectableWorkspace` (`tools_api.go:143`), whose contract
is "find an owner workspace by UUID (preferred) or by name, restricted to workspaces owned
by a chat participant".

## Root cause (candidate #1, proven from the integration source)

Because the 400 is rendered with a **non-empty hint (`"open-chat-go"`)**, candidate cause 2
("`tool_init` never bound anything on this dispatch path") is disproven: a binding *was*
present. What failed is the *resolution* of that binding.

In `open-chat-go-msgmate-coder@main`, `workspace.go:716-735` (`resolveCoderContextOpts`)
takes the configured default hint:

```go
hint := coderConfigString(config, coderConfigWorkspaceUUIDKey)   // coder_workspace_uuid
if hint == "" {
    hint = coderConfigString(config, coderConfigWorkspaceKey)    // coder_workspace -> "open-chat-go"
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

So the binding is configured (hint = `"open-chat-go"`, the *name*, matching the dispatch
tag `repo:open-chat-go`), and the read path resolves it against
`owner_user_id IN (chat.User1Id, chat.User2Id)` and misses — the workspace row is owned by
a bot/service account (or an admin-only kaniko scope) that is not a chat participant.

Compounding it: the ci#526 auto-provision fallback right below (`workspace.go:736-751`) is
guarded by `if workspaceUUID == ""`, so a *failed* hint resolution returns the 400 **before**
the fallback is ever considered. A stale or mis-owned configured default therefore
dead-ends every tool — exactly the ci#399/ci#429 "hard-fail instead of
resolving/provisioning" class.

## Fix (must land in `open-chat-go-msgmate-coder`)

1. `resolveSelectableWorkspace`: keep the participant-scoped lookup first (that is the
   authorization boundary for a *user-supplied* selection), then, for the **configured
   default** path only, add an owner-relaxed fallback lookup by UUID then by name (still
   `deleted_at IS NULL`). Names are unique per kaniko/provisioning scope, so this resolves
   `"open-chat-go"` to the row the chat is actually meant to use.
2. `resolveCoderContextOpts` (`workspace.go:728-734`): treat an unresolvable **configured**
   hint as "no binding" (log it) and fall through to the existing ci#526 per-chat
   auto-provision fallback instead of returning a 400. An unresolvable *explicit user*
   selection (`handleSelectWorkspace`, `api_handlers.go:337`) must keep returning the
   error, so a typo is still reported.
3. Persist workspace identity by UUID wherever possible; names are display data.

## Regression tests to add with the fix

Extend the sqlite harness in `workspace_resolve_test.go` (`newCoderTestDB`,
`newCoderTestChat`):

- a chat whose `coder_workspace` names a workspace owned by a *non*-participant must
  resolve to that workspace UUID (fails on current `main`: 400);
- a chat **with** a binding must resolve it by UUID *and* by name;
- a chat with **no** binding and no configured default must auto-provision its per-chat
  sandbox and must not surface `workspace %q not found for this chat` from a read-only
  `coder_list`;
- an explicit unresolvable selection still errors (guard against over-relaxing).

## Core-side follow-up worth checking alongside the fix

`backend/agentdefaults/taskrepo.go` (`WorkspaceIdentityConfigKeys`, `RepoSlug`) and
`backend/api/bots/interaction_tool_init_pins.go` reconcile server-owned pins and strip
identity keys. Confirm the seeded `tool_init` pin carries `workspace_uuid` (or an
`owner/name` slug) rather than a bare repo name, so the integration is not handed an
unresolvable hint in the first place.

## Why no PR was opened against this repo

The change has no core-side component: the hint is *read* by the integration, not written
by core (`coder_workspace` does not exist on `origin/dev`). A PR to `open-chat-go` `dev`
for this fix would be empty or unrelated, so none was fabricated. This branch is a
docs-only record of the blocker and the patch spec; the fix belongs in
`open-chat-go-msgmate-coder` (its only branch is `main`; the `dev`-trunk + `Closes`/label
process described in this repo's `DEVELOPMENT.md` does not apply there verbatim).
