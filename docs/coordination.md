# Continuous coordination (ci#747)

Umbrella contract for the failure pattern that kept the development lead and the
workers it dispatches idling instead of driving the tracker to completion: *the
lead reports status, the turn ends, and nothing re-triggers it*.

This document is the in-repo, reviewable half of the fix; the runtime half lives
in `backend/api/msgmate`.

## 1. A turn must not end while actionable work remains (RC6)

- A status report is **not** a valid terminal state while the ticket still has
  open acceptance criteria.
- Every turn ends with either an explicit next action (the tool call that
  performs it) or a sentinel (`<<TASK_COMPLETE>>` / an explicit blocker).
- Reconcile the tracker as part of every run: re-read the open work you own
  before reporting, so the report cannot claim progress the tracker does not
  show.

## 2. Self-resume: the loop re-fires itself (RC1)

The declarative schedules in `OPEN_CHAT_SELF_MESSAGE_SCHEDULES` are seeded at
boot by `msgmate.StartSelfMessageRuntime` (wired in `backend/cmd/server.go` and
`backend/cmd/worker.go`). A configured actor therefore re-fires its own loop;
its continuity does not depend on an external dispatcher. `SetEnqueue`/`SetCancel`
are bound to the real dispatch/cancel path and the task id is stable per bot, so a
newer schedule cancels the previous task instead of adding a second one.

## 3. A cap or a transient failure continues exactly once (RC2, RC3)

`msgmate.CapContinuationForOutcome` decides, purely, whether a finished turn is
continued:

| turn ended because | continued |
| --- | --- |
| failed-tool-call cap | yes, once |
| total tool-call cap | yes, once |
| provider error *with* a transient failure | yes, once |
| provider error without a transient failure | no - stay explicit |
| completed / cancelled / confirmable pause | no |
| second attempt of the same chain | no |
| unidentified chain | no |

The continuation carries a stable, chain-scoped correlation id
(`ci747-cont-<chain>`), so a repeated decision cancels instead of duplicating.
A retry a retry cannot fix is *never* masked: the failure stays explicit in the
interaction and in the tracker.

The prompt forbids repeating the calls that burned the budget, which is what
turned a degraded sandbox/gh/API into a hard failure in the first place.

## 4. One owner per ticket lane (RC7, QW4)

`msgmate.CoordinationDedupeKey(ticket, lane)` derives the owner key
(`owner:<ticket>:<lane>`) used by every dispatch path - the workflow route, the
self-message schedule and the reconcile step. Two dispatches of the same lane of
the same ticket collapse onto one owner; two lanes of one ticket stay distinct.
The key is lowercased and whitespace-normalised, so `ci#747`, `CI#747` and
`ci#747 ` are one owner. An empty ticket yields no key and must be refused rather
than inventing an owner.

## 5. Identity and permissions (RC5)

The lead observes interactions it does not own: `hive_mind_agent_continue`,
`hive_mind_agent_get` and `hive_mind_stop` return 404 for a chat owned by another
account (`open-chat-go-admin`), so the lead can neither steer nor de-duplicate it
directly.

Minimum scope the lead needs, and the route it must use:

- **stop / resume a chat it does not own:** the GitHub workflow route
  (re-dispatch), which needs no chat ownership.
- **de-duplicate an owned ticket:** `CoordinationDedupeKey` (section 4) plus the
  tracker, not direct chat steering.
- **operator API session:** the lead's token is currently `Forbidden`; the gap
  (which scope is missing) stays tracked on ci#747.

No secrets, tokens or user data are recorded here by design.

## 6. Live canary assertions (QW3, RC4)

PR CI must fail loudly, not a user, when either invariant regresses:

1. **enqueue-after-archive must not 500** - replying to an archived task must
   return success/queue (regression guard for the 0.0.744 hotfix).
2. **self-resume must fire** - a scheduled actor re-fires once within one
   interval.
