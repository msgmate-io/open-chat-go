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

## Reading a finished deployment run (rotation verdict)

The capability check above gates a candidate credential *before* it is stored.
The other half of ci#1044 is the after-the-fact question: did the deploy hop
actually run on the profile's vault token, or on the ci#1042 CI-secret fallback
that keeps it green?

`development/scripts/verify_deployment_bot_token_rotation.py` answers that from
the run's own evidence (log + step conclusions), read-only and network-free:

```bash
gh run view <run-id> --repo msgmate-io/open-chat-go --log > /tmp/run.log
gh run view <run-id> --repo msgmate-io/open-chat-go --json jobs > /tmp/run-jobs.json
python3 development/scripts/verify_deployment_bot_token_rotation.py \
    --log-file /tmp/run.log --jobs-json /tmp/run-jobs.json
```

It reports the three acceptance criteria of ci#1044:

| criterion | source | fails when |
| --- | --- | --- |
| 1 token source is the vault `BOT_TOKEN` | `Using the bot token from …` summary line | the summary names `CI repository secret BOT_TOKEN`, or a `::warning::` names the vault item as rejected |
| 2 precondition assertion passed | `Resolve and verify GitHub bot token` step | the step is not green, or the log carries `bot token cannot read <owner/repo>@<ref>` |
| 3 deploy + `VERSION` assertions green | `Deploy Helm chart`, `Assert deployed VERSION matches the merged VERSION` | either step is not green |

Exit code is `0` only when all three are `OK`. A run with no summary line or no
such steps is reported `INCONCLUSIVE` (exit `1`), never as rotated — a fallback
deploy is deliberately *not* treated as a completed rotation. The report carries
verdicts, step names and the token-source label only; a unit test asserts a
credential present in the log never reaches it.

Verification of the running deployment itself still goes through `Live Runtime
Canary` (`repository_dispatch: live-canary`) and the log-monitoring workflow —
this script reads run metadata, and neither mutates the cluster nor touches the
vault.

## `--plan`: the outstanding re-issue request

ci#1044 is a credential task, so no code change can close it. What the scripts can
do is keep the *request* accurate. `--plan` renders the outstanding work from a
real run's evidence instead of only a verdict:

```bash
python3 development/scripts/verify_deployment_bot_token_rotation.py \
    --log-file /tmp/run.log --jobs-json /tmp/run-jobs.json --plan
```

The plan prints the observed token source, the vault rejection status with its
status-specific guidance (`401` = dead credential, re-issue; `403` = capability or
rate limit; `404` = the token's selected-repositories list excludes
`msgmate-io/open-chat-go`), the two profiles whose `BOT_TOKEN` item has to be
rewritten, the capability set the replacement must carry, and the two commands that
prove it (capability check before storing, rotation verdict after the next
deployment). It reports `No re-issue outstanding` only when the same run already
satisfies all three acceptance criteria, and it never sees a credential, so a plan
cannot leak one.

## Re-issuing the vault `BOT_TOKEN` (maintainer procedure)

The rotation itself is a credential action on the vault and cannot be done from
CI or from an agent sandbox; every code-side half of ci#1044 is in place to make
it provable before and after the fact.

1. Create a **fine-grained** PAT (`github_pat_…`) with resource owner
   `msgmate-io` and the *selected repositories* list containing
   `msgmate-io/open-chat-go` (a token scoped to another org repo answers 404 and
   reproduces the `git` exit 128 of run `42959618854`).
2. Grant `Contents: read`, `Pull requests: read and write`, and
   `Packages: read and write` (the deploy pulls/pushes
   `ghcr.io/msgmate-io/open-chat-go:pr-<n>-<sha>`).
3. Prove the candidate **before** storing it:
   `python3 development/scripts/check_bot_token_capabilities.py --token-file <file>`.
   It must print no `MISSING` row; `pull_requests:write` stays `UNVERIFIED` by
   design and is settled by the deploy's preview-URL comment.
4. Write it into the `BOT_TOKEN` item of the `msgmate-io-pr-deployment` and
   staging SecretSpec profiles. Never paste it into an issue, PR or commit.
5. Confirm the rotation on the next `[Dev promotion]` deployment:
   `python3 development/scripts/verify_deployment_bot_token_rotation.py --log-file … --jobs-json …`
   must print `ROTATED`. Until then the run stays green on the CI-secret
   fallback and the report stays `NOT ROTATED` — a working state, not a correct
   one.

Current state at the time of writing: the vault item answers
`HTTP 401 - invalid, expired or revoked` (run `43130639468`), so the fix is a
re-issue, not a scope change; the CI-secret fallback is deliberately kept.

Verification of the deployed runtime still goes through `Live Runtime Canary`
(`repository_dispatch: live-canary`) and the log-monitoring workflow.
