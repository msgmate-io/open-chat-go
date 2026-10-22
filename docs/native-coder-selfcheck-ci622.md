# Native-coder self-check regression — ci ticket #827 (ci#622)

Triage note for the per-role native-coder self-verification suite of
`msgmate-io/open-chat-go-ci` (`native-coder-selfcheck.yaml`, tracker ticket #827).

## Reported failure

- Run `39750438046`, source `75153b0ccf738bb178f691ef143bb5f3698301c1`, **0/5 roles passed**.
- All five roles (`plan`, `repl`, `review`, `consolidate`, `development-lead`)
  report the *same single* failure mode: `✗ record_collected — no record was collected`.
- The **Interaction column is empty (`-`)** for every role, i.e. no interaction /
  chat uuid was recorded for any of them.

## Reading of that failure mode

- `record_collected` is the harness-level check that a role job's record
  (`record-<role>.json`) reached the aggregator; "no record was collected" means
  the role jobs never produced an uploadable record.
- An empty interaction column means the jobs also never recorded a chat uuid.
  Combined, every role job died at (or before) the dispatch step, i.e. **before
  any agent turn ran** — this is not a per-role behaviour verdict, and the fact
  that all five roles fail identically in a single run points at one shared,
  request-level cause in the deployment under test rather than at five
  independent role regressions.
- The harness binds the run to a fixture repository (`tool_init` carries
  `git_repo: https://github.com/${FIXTURE_REPO}.git`), so the dispatch payload
  depends on the fixture-repo and tool_init handling of the deployed revision.
- A previous run of the same suite dispatched normally, so this is a regression
  in the environment/host reached by the workflow, not a change in the harness
  plumbing that this repository does not contain.

## Where the server side is implicated

`backend/api/bots/Bots.go` owns both halves of the dispatch request that the
harness relies on:

1. **Identifier resolution** — `resolveReadableBot` (`Bots.go:824`):
   uuid lookup, then a *name* lookup that is owner-scoped for non-admins
   (`Bots.go:832` requires owner-or-admin-or-public, `Bots.go:840` applies
   `runtimeIDsOwnedByUserSubquery`, defined at `Bots.go:196`), then a
   username fallback. The CI dispatches the coder-fleet bots by identifier; when
   the runtime configs for those bots are neither public (`IsPublic`,
   `Bots.go:74/105`) nor owned by the CI service account, resolution fails and
   the handler answers `404`.
2. **`tool_init` acceptance/merge** — the per-tool-key override that lets the
   harness replace the workspace selector (`coder_select_workspace`) with the
   fixture repo. A revision that rejects or ignores the override changes the
   dispatch outcome for *all* roles at once, which matches the observed
   signature.

## Verification steps (next data point)

1. Re-dispatch one role (e.g. `plan`) against `OPEN_CHAT_HOST` and capture the
   HTTP status **and response body** of the interactions POST. The status/error
   tells apart "bot not resolvable (`404`)" from "tool_init rejected (`4xx`
   validation)".
2. If it is the resolution arm, re-seed the five coder runtime configs as public
   and/or owned by the CI service account.
3. If it is the tool_init arm, fix the per-tool-key merge so a
   `coder_select_workspace` override replaces the whole selector value and an
   unset `mode` falls back to the bot default.
4. Re-run the suite; a passing run closes tracker #827 automatically.

## Scope of this change

Documentation/triage only. No behavioural change is made to server code here:
the decisive evidence (the dispatch response body, obtained with the CI secret)
is not available to this workspace, and any edit to the resolution/merge logic
without it would be guesswork. Credentials, tokens and kubeconfig are never
recorded here.
