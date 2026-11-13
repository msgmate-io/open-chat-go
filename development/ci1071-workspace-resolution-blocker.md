# ci#1071 — canary: coder_list cannot resolve the chat workspace (investigation blocked)

Blocker for this run: the coding sandbox is missing the coder integration
checkout (`clients/integrations/` is empty; `community_coder_integration` is not
materialized and no Go module cache is present), so the code that resolves a
workspace *for a chat* (`workspace "open-chat-go" not found for this chat`, a 400
raised by the coder tool API) is not in this tree and the cause cannot be proven
from source here - only guessed. Per the ticket instruction (prove, do not guess),
no speculative patch was applied.

What is verifiable from core (`dev`) so far:

- `backend/api/bots/Bots.go:1546` seeds `tool_init` for init-requiring tools at
  chat start via `agentdefaults.ApplyAgentInteractionDefaults`, and
  `interaction_tool_init_pins.go:192` stamps the server-owned pins afterwards.
  Neither binds a per-chat workspace *identity* - the selector is seeded with
  `git_repo`/`auto_sandbox` only (see `backend/agentdefaults/taskrepo.go:236`
  `pinsProvisionedWorkspace`).
- `backend/api/msgmate/agent_runtime.go:191` `mergeAgentToolInit` lets a turn
  spec `ToolInit` win and only lets the `PreCallInit` hook fill absent keys, so a
  stale pin from chat-start is never refreshed mid-turn.

Next steps (need the coder integration source):

1. Reproduce with a unit test against the coder tool API binding: a chat with a
   workspace binding must resolve; a chat with no binding must not 400 on a
   read-only call (auto-provision or default instead).
2. Check whether the chat-start seeding writes the workspace by *name* while the
   read path looks it up by chat-scoped UUID (candidate cause 1), or whether
   `tool_init` never binds on the agent-dispatch path (candidate cause 2, the
   ci#399/ci#429 regression class).

Re-provision the sandbox with the integration submodules and rerun.
