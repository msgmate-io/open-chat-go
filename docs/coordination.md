# Continuous coordination

Human-facing pointer for how the automated development agents keep working
without a human re-dispatch. The operational runbook itself is agent-only and
lives in the private integration repository
`msgmate-io/open-chat-go-msgmate-coder` (`docs/agent/`), where it is served to a
native `msgmate_coder` interaction at runtime by its `coder_docs` tool.

In short:

- an idle agent is re-fired by its own declarative schedule instead of waiting
  for a human to dispatch it again;
- a turn that runs out of tool calls degrades to a partial report plus exactly
  one automatic continuation, and never masks an unrecoverable failure;
- a transient provider or workspace failure is retried with bounded backoff and
  the retry stays visible in the interaction;
- one lane of one ticket has exactly one owning interaction; a duplicate
  dispatch is refused rather than started;
- stopping or resuming an agent the operator does not own goes through the
  GitHub workflow route, which needs no chat ownership.

No secrets, tokens or user data are recorded here by design.
