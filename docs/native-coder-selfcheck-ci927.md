# Native-coder self-check regression (ci#927)

- Run: 41298594098 — result 2/5 roles passed.
- Source: 86f828261066d05c26697e3bcb8b0c8fa0827dee.

| Role | Result | Checks |
| --- | --- | --- |
| `plan` | fail | `plan_comment_posted` — plan bot posts a plan comment on the fixture issue |
| `repl` | pass | — |
| `review` | fail | `interaction_finished` (state=failed), `no_agent_error` (agent turn reported an internal error), `review_submitted` |
| `consolidate` | pass | — |
| `development-lead` | fail | `dev_lead_report` — dev-lead bot produces the triage/dispatch/consolidate report |

## Fixes landed

- The plan/review workspace-dispatch regressions are pinned on `dev` (the
  2026-11-01 consolidation, r1, #1275):
  - `backend/nativecoder`: `TestCI927EmptyAutoSandboxToolInitBindsTheTaskRepo`,
    `TestCI927DispatchWinsOverTheConfigDefault` — the tool-init-default
    auto-sandbox path must bind the dispatched task repo, never the config
    default monorepo.
  - `backend/api/bots`: `TestCI927ReviewPublishesAsPullRequestReview` — the
    review post-action publishes as a pull-request review, not an issue
    comment.
- `backend/docguard` guards this per-run record (docs are part of the gate a
  suite run needs green).