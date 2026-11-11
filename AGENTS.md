# AGENTS.md

Working notes for coding agents on this repo. Setup, build and publishing
details live in `DEVELOPMENT.md`; this file only records routes an agent must
not hand-roll.

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
