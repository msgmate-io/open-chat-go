# Continuous coordination (ci#747)

Short, human-facing summary. The operational contract that the development bots
themselves follow is **not** kept in this repository: it lives in the private
integration (`msgmate-io/open-chat-go-msgmate-coder`, `docs/agent/`) and is served
to a native `msgmate_coder` interaction at runtime by that integration's
`coder_docs` tool (`action: "list"` to see the pages, `action: "read"` to fetch
one). `backend/docguard` fails the build if agent-only sections reappear here
(ci#736), so quote pages through `coder_docs` instead of copying them back.

## What is public here

- The queue/self-resume runtime itself: the declarative schedules in
  `OPEN_CHAT_SELF_MESSAGE_SCHEDULES` are seeded at boot by
  `msgmate.StartSelfMessageRuntime` (wired in `backend/cmd/server.go` and
  `backend/cmd/worker.go`). A configured actor re-fires its own loop, so
  continuity does not depend on an external dispatcher. The schedule bookkeeping
  lives in `backend/api/msgmate`; the task id is stable per actor, so a newer
  schedule cancels the previous task instead of adding a second one.
- The safety property that a misconfigured actor degrades instead of breaking a
  deployment: an unseeded or unknown configured actor only produces a loud log
  line, and the backend keeps serving (`ci#744`/`ci#785`). See
  `backend/cmd/self_message_wiring_test.go`.
- Bounded continuation after a cap or a transient failure, and the per-turn tool
  budget defaults in `backend/api/msgmate` (`DefaultToolCallMaxTotal`,
  `DefaultToolCallMaxFailed`; ci#709).
- One lane of one ticket has exactly one owning interaction; a duplicate
  dispatch is refused rather than started.
- Stopping or resuming an agent the operator does not own goes through the
  GitHub workflow route, which needs no chat ownership.

## Where the operator playbooks live

Fleet moderation, the pass playbooks, the toolkit reference, which providers a
fleet may route to, budget/compaction rules, the label and CI-failure routing
policies: all of that is agent-facing and therefore private. Maintainers get it from the private
integration repository, or by asking a coder agent to quote a page through
`coder_docs`. Do not add those sections back to this file.

No secrets, tokens or user data are recorded here by design.

## Continuous coordination (ci#747)

Binding for the development lead and every worker agent it dispatches. The
failure mode this section exists for is not a wrong answer: the lead reported
status, the turn ended, and nothing re-fired it.

1. **Never end a turn on a status report while actionable work remains.** A
   progress summary is not a turn terminator. If work you own is still open (a
   ticket, your PR, a failing check on your PR), continue in the same turn.
2. **End every turn with an explicit next action or the completion sentinel.**
   Name the concrete next action (what, by which actor, when it becomes visible)
   or finish. An interaction that ends `waiting` with neither is a run bug.
3. **Reconcile the tracker on every run.** List the work you own (tickets,
   lanes, PRs) and act on the difference: re-dispatch what stalled, stop what is
   duplicated, close what is done. The previous run's state is not assumed.
4. **One owner per `(ticket, lane)`.** Derive the coordination dedupe key from
   ticket and lane *before* dispatching and refuse a second interaction for a key
   that already has an owner (QW4). Two agents on one lane fight over the same
   branch and PR; a duplicate owner is worse than a slow one.
5. **Transient infra is not "done".** A provider 402/429/5xx, a saturated
   workspace or an exhausted failed-tool-call budget ends the turn as a
   *retryable* state: emit a partial summary plus the blocker, and enqueue
   exactly one continuation through the in-product `coder_schedule` path with the
   stable chain correlation id, so a newer decision cancels instead of
   duplicating the previous task (RC2/RC3). Never silently stay `waiting`.
6. **Make the retry visible.** Record the continuation reason, correlation id and
   schedule id in the interaction, so a human can tell an automatic resume from a
   fresh dispatch.

### Lead identity and minimum scope (RC5)

The lead acts through its GitHub identity and the dispatch workflow; steering
every chat directly is not assumed.

- **Minimum scope:** read issues/PRs/checks, comment on issues and PRs, dispatch
  and stop the agent workflows, list its own interactions.
- **Ownership gap:** interactions owned by another account (eg
  `open-chat-go-admin`) are not steerable with the lead's token -
  `hive_mind_agent_continue`, `hive_mind_agent_get` and `hive_mind_stop` return
  `404`. The primary stop/resume route is therefore the GitHub workflow dispatch,
  which needs no chat ownership.
- **Operator API:** the operator API session is `Forbidden` for the lead's token.
  This gap is folded into ci#747 (no separate ticket); the minimum scope above is
  what the lead needs to work around it.

### Live-canary expectations (QW3)

The live canary must fail loudly, not quietly, on the two relapses ci#747 was
filed for:

- enqueuing a bot reply for an **archived** task returns success/queued, never
  `500` (RC4);
- a scheduled actor (the lead's declarative self-message schedule, ci#744) must
  re-fire **once within one interval** (RC1).
