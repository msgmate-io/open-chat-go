# AGENTS.md

Working notes for coding agents on this repo. Setup, build and publishing
details live in `DEVELOPMENT.md`; this file records the routes an agent must not
hand-roll and the pull request conventions a change has to satisfy to be
mergeable.

## GitHub writes: prefer typed tools

Prefer the typed GitHub tools (`github_pr_view`, `github_pr_edit`,
`github_pr_comment`, `github_issue_comment`, `github_pr_create`,
`github_pr_merge`, ...) over `gh` in `coder_bash`. A typed write is re-read and
reported as `verified:true/false`, so a silent no-op cannot pass as done.

### `gh pr edit` is broken in the coding sandbox

The per-chat sandbox GitHub token has no `read:org`, so every `gh` command that
resolves org membership fails:

```
gh pr edit 2 --repo msgmate-io/open-chat-go --body-file /tmp/body.md
→ GraphQL: Could not resolve to a Organization with the login of 'msgmate-io' (read:org)
```

Use `github_pr_edit` instead (ci#1035). It routes the same edits over REST —
`PATCH /repos/{owner}/{repo}/pulls/{n}` plus
`POST /repos/{owner}/{repo}/issues/{n}/labels` and
`DELETE /repos/{owner}/{repo}/issues/{n}/labels/{name}` for labels — so it never
depends on org-membership resolution.

If a `gh` path is genuinely the only option, fall back to REST by hand
(`gh api -X PATCH repos/<owner>/<repo>/pulls/<n> -F body=@/tmp/b.md -f base=dev`)
rather than closing and re-opening a pull request.

### Trunk policy

The trunk for this repo family is `dev`: open pull requests against `dev`, and
`github_pr_edit` refuses a base retarget away from `dev` unless
`allow_base_retarget` is set.

`main` is only ever advanced by the automatic `[Dev promotion]` pull request
(`dev` -> `main`), which the CI automation creates, refreshes in place and
merges. A feature branch based on `main`, or a pull request whose base is
`main`, is a defect: it skips the integration trunk and would deploy directly.
Never push to `dev` or `main`, and never create, retarget, relabel, close or
merge the promotion pull request (ci#833, ci#931).

- Branch off the *fetched* trunk, not a stale local copy:
  `git fetch origin dev` then `git checkout -B <branch> origin/dev`.
- Retargeting a mis-based pull request is an emergency repair, not the plan.
  Get `base=dev` right at creation time - `github_pr_create` takes the base and
  a later retarget costs a full CI cycle.

## Pull request conventions (ci#1032)

Every agent-authored pull request must satisfy all five checks below *before*
the run is reported as done. Each of them has already cost a manual repair and
a wasted CI cycle, so treat a miss as a failed run, not a nit.

| Check | Required value |
| --- | --- |
| Base branch | `dev` |
| State | **not a draft** |
| Labels | `test` for a feature/bugfix/docs/chore PR; `test`,`e2e` for `chore/consolidate-*` |
| Tracker line | a line of its own: `Closes msgmate-io/open-chat-go-ci#<n>` |
| Verification | truthful: say what actually ran, locally or not at all |

### Never open a feature PR as a draft

A draft pull request reports the run as successful and then blocks the merge
with *"Pull Request is still a draft"*. Open feature, docs and chore pull
requests as ready-for-review - or, if you opened a draft to get the number
early, mark it ready (`github_pr_edit`) before you report it. A draft number in
the final summary is a failed run, not a partial success.

### Labels: one class label, no heavy suite

- A feature/bugfix/docs/chore PR against `dev` carries the ordinary
  code-change label **`test`** and nothing else. Do not add `deploy`,
  `sandbox-test`, `build`, `e2e`, `test-core-only`, `ai-test-mock` or
  `ai-test-real`: those belong exclusively to the automatic `[Dev promotion]`
  pull request, and adding one here dispatches a deployment for a PR that is not
  the promotion hop.
- A consolidation PR (`chore/consolidate-*`) carries `test` **and** `e2e`.
- `dependencies` and `no-issue` are exemption labels with a narrow meaning; do
  not use them to route around the tracker line.

### The tracker line is a line, not a sentence

`PR Test Merge Requirement` reads the body with `development/scripts/linked_ci_issues.sh`
and only accepts a line whose entire content is that reference - optionally
wrapped in markdown (a `- ` bullet, `**bold**`, a `| table cell |`) and
optionally ending in punctuation. A prose mention such as
`Implemented for (#1031)` does **not** satisfy it, and a feature/bugfix PR must
close **exactly one** leaf issue.

Put it under a `## Tracker` heading (the pull request template already has one).
The body has to be right the first time: `gh pr edit` cannot be used here, so a
fix is `github_pr_edit` (or `gh api -X PATCH repos/<owner>/<repo>/pulls/<n> -F body=@/tmp/body.md`).
The line is free text in the body - editing it needs no new commit.

Consolidation (`chore/consolidate-*`), dependency and `no-issue` pull requests
are exempt from the tracker line; a `test`-labelled, test-only PR is exempt too.
Do not lean on the exemptions for ordinary work - link the leaf issue.

### Report tests truthfully

The coder sandbox image ships a Go toolchain pinned to `backend/go.mod` with a
warmed module cache, so `go build ./...` and targeted `go test ./<pkg>/...` are
expected to work offline after checkout. Any other suite (full `go test ./...`,
E2E, image builds) is still CI-only.

**If `go` is missing, do not claim tests passed.** Run the readiness probe:

```bash
development/scripts/check_coder_sandbox_readiness.sh
```

When it reports `missing_cli: go` / `go: MISSING`, state `go: MISSING` in the
pull request body and push only behind the CI gate. The probe also runs
`GOPROXY=off go build ./...` itself and reports `ok   go build ./... succeeds
offline` or `MISS go build ./... fails offline`; quote that line instead of
describing a build from memory.
`development/scripts/install_sandbox_go_toolchain.sh --check` is the self-check and
`go: MISSING` is the only acceptable report when it fails; repair with the same
script (never a manual `export PATH`) and re-run it before claiming anything.
In the pull request body (and
in the final summary) state exactly which command ran and in which environment,
or say that nothing ran and that PR CI is the gate. Claiming a suite that did not
run is worse than reporting an untested change: PR CI is authoritative and nobody
has to trust the claim. No polling, no "CI is green" unless a check-run read
actually said so.

### A branch without a pull request is not a deliverable

A run is finished when the branch is **pushed and the pull request number
exists** - not when the diff looks right. Ending a turn with
"changes are ready" over an unpushed branch, or with nothing committed at all,
wastes the whole dispatch: the work is invisible to the merge gate, to the
promotion PR and to a reviewer.

- Commit before you summarize; a clean working tree is the precondition.
- Push, then create the pull request in the same run.
- The summary names the pull request (`#N` and its URL). Without a number,
  nothing in the run is durable.
- If you genuinely cannot open it (auth failure, conflict you must not resolve),
  say so explicitly and leave the pushed branch behind - silence is the failure
  mode, not the blocker.

Before reporting, re-read the pull request with `github_pr_view` and confirm
`base=dev`, `draft=false`, the label set and the body. Do not rely on the
create call having echoed what you asked for.

## Merging the `dev` -> `main` promotion PR is allowed, verifying it is required

The `[Dev promotion]` pull request is special: merging it is the deployment hop
to ci.msgmate.io, and it is created and refreshed by CI automation - never create,
retarget, relabel or close it yourself. But you *may* merge **that** pull
request once every required check on it is green (ci#1055); no maintainer click
is needed for the loop to close. Decide with the tested predicate instead of
reading API payloads by eye:

```bash
development/scripts/promotion_hop.sh merge              # 0 merge, 1 wait, 2 refuse
development/scripts/promotion_hop.sh deploy <VERSION>   # 0 healthy, 1 broken
```

`merge` refuses anything that is not `dev` -> `main`, and it collapses duplicate
check-runs onto the **newest** instance per (name, app) before deciding - a
cancelled label-event duplicate leaving a stale `PR Test Merge Requirement =
FAILURE` on the commit is *not* a red gate (blocker 2 of ci#1055), and the
instances it disregarded are printed as `ignored superseded check-run instances`.
Exit `1` means "not yet" (a replacement run still has to conclude, or the branch
is `BEHIND` - use `github_pr_update_branch`); exit `2` means a real red or a
structurally wrong hop, so do not merge and report it.

After the merge, run `deploy` and report the deployed VERSION. A successful
rollout is not acceptance: a ticket with a behavioural criterion closes only once
its feature has been observed working on ci.msgmate.io, and a deployment serving
an older (or unmerged-branch, ci#890) VERSION is a breakage to ticket, not a note.

## A green PR that is "behind" the base branch

When a required gate (for example `PR Test Merge Requirement`) fails **only**
because the pull request head is behind its base - `mergeable_state=behind`,
`mergeStateStatus=BEHIND` - the code is fine and the base branch simply moved.

Call the typed tool `github_pr_update_branch` (ci#1037):

- `number` (required) - the pull request number.
- `repo` (optional) - `owner/name`, defaults to the configured repo.
- `expected_head_sha` (optional) - optimistic guard: only update when the head
  is still at that commit.

It routes through the GitHub **REST** endpoint
`PUT /repos/{owner}/{repo}/pulls/{n}/update-branch`, so it never depends on
org-membership GraphQL resolution (`read:org`, cf. ci#1035).

Do **not** fix this by force-rebasing the branch, and do **not** close and
re-create the pull request: both restart the required gate for no code change.
Do not hand-roll a merge-and-push of the base into the head either; use the
typed route.

Read the answer instead of assuming success:

- `state: updated` - GitHub rewrote the branch; `verified: true` means the
  re-read shows a new head SHA.
- `state: already_up_to_date` - the 422 "already up to date" answer. This is a
  normal answer, not a failure.
- `state: merge_conflict` - the 422 "merge conflict between base and head"
  answer. Also not a failure of the call, but it needs a real rebase or a human.
- `verified: false` - the head did not actually move. `verification_error` says
  why (still `BEHIND`, unchanged head SHA, or the pull request could not be
  re-read). Never report the branch as updated in that case.

After a successful update the pull request is re-read and the answer carries
`mergeable`, `merge_state_status` and `head_sha`; use those values instead of a
second ad-hoc lookup.
