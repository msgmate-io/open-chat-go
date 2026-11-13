# ci#1071 — canary: `coder_list` cannot resolve the chat workspace

Status: root cause located in the **coder integration**, not in open-chat-go core. No
patch to open-chat-go core is justified by the evidence, so this branch carries the
diagnosis only.

## Evidence

The failing string is not produced by open-chat-go core:

- `git grep 'not found for this chat' origin/dev -- backend` -> 0 hits in core.
  Core only knows the ci#399/ci#429 class:
  `no workspace selected for this chat; call coder_select_workspace first`
  (`backend/api/tools/confirmable_actions_test.go:12`), which is a *different* message on
  a *different* code path.
- The message `workspace %q not found for this chat` is raised in
  `github.com/msgmate-io/open-chat-go-msgmate-coder` (`tools_api.go:163`), inside
  `resolveCoderContext` -> `resolveSelectableWorkspace` (`tools_api.go:143`), whose
  contract is "find an owner workspace by UUID (preferred) or by name, restricted to
  workspaces owned by a chat participant".

Because the error is rendered with a **non-empty hint (`"open-chat-go"`)**, candidate
cause 2 ("`tool_init` never bound anything") is disproven: a binding *was* present. What
failed is the *resolution of that binding*:

- the hint handed to the resolver is a bare repository/workspace **name**
  (`open-chat-go`, matching the dispatch tag `repo:open-chat-go`), not a workspace UUID;
- `resolveSelectableWorkspace` then matches it by name against workspaces **owned by a
  chat participant**; for an automated canary chat no participant owns a workspace with
  that name, so the lookup misses and the whole read-only call is failed with a 400.

So this is candidate cause 1: the binding is written in a form (name / repo slug) that
the read path cannot resolve, and a resolution miss is escalated to a hard 400 instead of
degrading.

## Patch spec (for the coder integration)

1. In `resolveCoderContext`, treat the pinned value as *either* a `workspace_uuid` or a
   workspace name / repo slug, and consult, in order: the explicit input override, the
   chat's bound session UUID, then the config hint resolved by UUID, by name, and finally
   by `git_repo` slug -> provisioned/default workspace.
2. Never let a name-resolution miss produce a 400 for a read-only tool: fall back to the
   chat default / auto-provisioned workspace (or answer
   `no workspace selected; call coder_select_workspace`), i.e. keep the ci#399/ci#429
   behaviour of not hard-failing every tool call.
3. Persist workspace identity by UUID wherever possible; names are display data.

## Regression test to add with the fix

- a chat with a workspace binding must resolve that workspace (by UUID *and* by name);
- a chat with no binding must not 400 on a read-only tool call.

## Core-side follow-up worth checking alongside the fix

`backend/agentdefaults/taskrepo.go` (`WorkspaceIdentityConfigKeys`, `RepoSlug`) and
`backend/api/bots/interaction_tool_init_pins.go` (`fit`) reconcile server-owned pins and
strip identity keys. Confirm the seeded `tool_init` pin carries `workspace_uuid` (or an
`owner/name` slug) rather than a bare repo name, so the integration is not handed an
unresolvable hint in the first place.
