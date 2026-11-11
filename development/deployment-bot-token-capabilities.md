# Deployment-profile `BOT_TOKEN` capabilities (ci#1044)

ci#1042/ci#1043 made the CI-side deploy survive a stale per-deployment vault
`BOT_TOKEN`: every candidate is probed before `actions/checkout` runs, the
rejected one is named, and the deploy continues on the CI-secret fallback. That
keeps deployments green, but it hides *which* capability the vault item is
missing and it reports only the first one it hits (`contents:read`), so a token
that is also missing `packages:*` or `pull_requests:write` fails one deploy step
later, mid-run.

This note is the issuance-time side of the same contract: what the
deployment-profile `BOT_TOKEN` must be able to do, and how a candidate token is
proven to do it **before** it is stored. The implementation is
`development/scripts/check_bot_token_capabilities.py` (unit tests in
`test_check_bot_token_capabilities.py`, run by the PR CI dev-scripts job).

## Required capability set

| capability | needed by |
| --- | --- |
| `contents:read` on `msgmate-io/open-chat-go` | `actions/checkout` of the deploy target |
| `pull_requests:write` on `msgmate-io/open-chat-go` | commenting the preview URL back to the source PR |
| `packages:read` on `ghcr.io/msgmate-io/open-chat-go` | pulling the `pr-<n>-<sha>` images |
| `packages:write` on `ghcr.io/msgmate-io/open-chat-go` | pushing/manifesting those images |
| **fine-grained** PAT (`github_pat_…`) | whose selected-repositories list includes `msgmate-io/open-chat-go` |

The last row is not a nicety: a token scoped to a *different* repository of the
same organisation is not rejected with 403, GitHub answers the read with 404 and
`git` exits 128 — exactly run `42959618854`. A classic `ghp_` token with org-wide
`repo` scope is likewise wrong for a per-deployment profile: it holds more access
than one deployment should.

## Three verdicts, one of them fatal

| verdict | meaning | effect |
| --- | --- | --- |
| `OK` | probed successfully | none |
| `MISSING` | proven absent, with the failing deploy step named | check exits non-zero |
| `UNVERIFIED` | cannot be settled by a read-only probe | reported, never fatal |

`pull_requests:write` is always `UNVERIFIED`: fine-grained PATs expose no
"can I write?" endpoint, and inventing one would mean a write request against a
real PR. Its functional probe is the deploy step that comments the preview URL —
i.e. ticket acceptance criterion 3, not this check.

## What is probed

* **shape** — the credential prefix, so a classic/app token is rejected before
  any request and without ever printing the value.
* **contents** — `GET /repos/msgmate-io/open-chat-go/commits/<ref>`; 200 proves
  the selected-repository list actually includes the repo, 404 names the git
  exit 128 cause, 401 names expiry/revocation.
* **pull requests** — `GET /repos/…/pulls`, the read the deploy needs before it
  can comment.
* **packages** — a GHCR token exchange for
  `repository:msgmate-io/open-chat-go:pull,push`. The `access` list in the
  response is the granted scope, so it proves `packages:read` and
  `packages:write` separately (a pull-only token is reported as missing `push`
  instead of passing silently). Status `0` (registry unreachable) is reported as
  MISSING, never assumed fine.

## Constraints honoured

* The credential is read from `--token-file`, `BOT_TOKEN_CANDIDATE` or
  `BOT_TOKEN` and is never formatted into any output: the report carries status
  codes, capability names and the token shape only. A unit test asserts a probe
  result and a rendered report contain no token substring.
* Usage is offline-checkable (`--observations-json`) so the matrix can be
  exercised without a credential, a cluster or a deployment run.
* Nothing here replaces the CI-secret fallback: re-issuing the vault item is a
  maintainer action on the vault, and the fallback stays as insurance.
