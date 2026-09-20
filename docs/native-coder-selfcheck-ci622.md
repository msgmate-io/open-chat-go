# Native-coder self-check suite (ci#622)

The per-role native-coder self-verification suite exercises the five coder
roles against fixture issues/PRs and verifies each role's observable output:

| Role | Check | Verifies |
| --- | --- | --- |
| `plan` | `plan_comment_posted` | plan bot posts a plan comment on the fixture issue |
| `repl` | pass/fail run record | repl bot answers the fixture prompt |
| `review` | `review_submitted` | review bot submits a review on the fixture PR (its turn must also finish without an internal agent error) |
| `consolidate` | pass/fail run record | consolidate bot produces the consolidated commit |
| `development-lead` | `dev_lead_report` | dev-lead bot produces the triage/dispatch/consolidate report |

The suite dispatches each role through the same bot-interaction plumbing as a
real user (tags, fixture repos, per-chat sandboxes). Every failing run updates
the tracker issue (ci#622) in place with the per-role results and closes it
automatically once a later run passes. Per-run records live next to this file
(`docs/native-coder-selfcheck-<issue>.md`).