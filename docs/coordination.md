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

## Lane ownership and reconcile (ci#747 RC7 / QW4)

One lane of one ticket has exactly one owning interaction. The owner key is
derived by `backend/coordination` (`OwnerKey`/`OwnerKeyTag`), which is the single
definition the dispatcher stamps and the reconcile step reads:

- the key is `owner:<ticket>:<lane>`, lowercased and whitespace-normalised, so
  `ci#747`, `CI#747` and `ci#747 ` are the same owner and cannot create a second
  interaction;
- an omitted lane is the `default` lane; different lanes of one ticket are
  deliberately separate owners;
- a dispatch that names no ticket has no owner key and is never de-duplicated -
  it is reported as unidentified instead of silently adopting an unrelated lane.

`coordination.Reconcile` runs the reconcile part of every run: it maps
interactions onto owner keys, keeps the live interaction as owner of a contested
lane (deterministic tie-break, so the report cannot flap between runs) and
reports every duplicate lane and every unidentified interaction. A non-clean
report is the signal to re-dispatch the owned ticket through the GitHub workflow
rather than to create another interaction.

Minimum identity scope: an owner may always list, continue and stop the
interactions it dispatched; de-duplicating a lane it does not own is done through
the workflow re-dispatch route, so no direct chat ownership is required.
