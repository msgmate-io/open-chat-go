# Agent notes

Short operational rules for agents working in this repository. Setup, build and
publishing details live in `DEVELOPMENT.md`.

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
