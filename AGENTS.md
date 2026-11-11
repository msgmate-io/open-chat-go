# AGENTS.md

Working notes for coding agents on this repo. The publishing procedure itself
lives in `DEVELOPMENT.md`; this file only records routes an agent must not
hand-roll.

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
