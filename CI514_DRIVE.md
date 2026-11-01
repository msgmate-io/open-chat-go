# ci#514 — Retire `coder_bash`: umbrella plan & drive record

> Ticket: https://github.com/msgmate-io/open-chat-go-ci/issues/514
>
> Status: **ACTIVE (plan shipped, phases 0–1 measured, phase 2 shipped, remainder open)**
> Plan of record: `backend/nativecoder/bashretirement.go` (`BashRetirementPlanSnapshot`)
> Measurement job: `backend/scripts/bash_retirement_report` (`go run ./backend/scripts/bash_retirement_report < calls.jsonl`)
> This record: `CI514_DRIVE.md` (spanning drive; superseded only by the plan file)

## 1. Vision

The native `msgmate_coder` should operate at a **finer granularity than a
shell**. `coder_bash` is today a universal escape hatch: it is the most-used
mutating tool, it is the main reason dev agents park on confirmations, it is
opaque to the sandbox/audit layer, and its output is unstructured text the
model then has to re-parse. The end state is that the coder **does not need
`coder_bash` for normal work**: every recurring capability is expressed as a
few well-thought-out, typed tools whose semantics come from the relevant
integration (GitHub, git, kubernetes, docker_sandbox, …), giving more control,
safer policy, structured results and fewer confirmation round-trips — while
keeping all functionality and performance.

This ticket is the umbrella plan: it defines the target tool surface and the
phases, tests and migration needed to retire `coder_bash` without losing
capability. Every drive increment must (a) reduce the residual shell share on
the measured metric, (b) never claim coverage the typed surface does not have,
and (c) keep the plan and its evidence machine-checked so a phase can never be
declared done while one of its criteria was never checked.

## 2. Evidence: the recurring shell shapes

Historical transcript analysis (classifying `coder_bash` / `coder_git` calls)
shows the dominant, recurring shapes are:

1. **GitHub CLI workflows** — the dev-agent bread and butter:
   `gh issue list --repo … --state open --json …`, `gh pr view/diff …`,
   `gh issue comment …`, `gh pr create/merge/review …`, `gh api …`,
   `gh auth status`.
2. **git workflows** — `git status/log/diff`, `git checkout -b fix/514`,
   `git add/commit/push`, `git stash`, guarded mutating verbs.
3. **Repo & build inspection** — `pwd/ls/cat/grep/rg/find`, `go test/build`,
   `npm`, `jq`, `sed/awk`, `tree/stat/file`, `cd <dir> && <work>`.
4. **Environment probing** — `command -v <bin>`, `type -p <bin>`, `env`,
   `which`, tool/version lookups.
5. **Scaffolding** — `echo marker > file`, `cat > file`, `printf … > file`
   (output redirection that _produces_ a file).
6. **Residual** — credentials (`gh auth token/login`), interactive sessions,
   unknown binaries, non-GitHub integration CLIs (kubernetes, docker_sandbox,
   cloud CLIs). These are the escape hatch share the plan must shrink phase by
   phase.

The classifier that makes this measurable lives in
`backend/nativecoder/bashretirement.go` (`ClassifyShellCommand`,
`ClassifyShellSegments`, `bashBinaryCategories`); a command the classifier
does not recognise lands in `residual_shell` and is never claimed as covered.

## 3. Target tool surface (typed tools, GitHub first)

The typed surface that replaces `coder_bash` for normal work:

- **git**: `coder_git` read/write family — status, log, diff, show, blame,
  remote (reads) and add/commit/branch/checkout/push/reset/stash/fetch/tag
  (mutating, refuses main, refuses empty commits).
- **GitHub**: `github_issue_*`, `github_pr_*`, `github_run_*`,
  `github_api` / `github_auth_status` typed tools, with a write-verification
  pass (`githubwriteverify.go`) so every mutating GitHub write is re-read and
  reported verified, never merely claimed.
- **workspace**: `coder_read`, `coder_list`, `coder_glob`, `coder_grep`,
  `coder_webfetch`/`coder_websearch` (reads); `coder_write`, `coder_edit`
  (mutating, structured).
- **policy bridges** that keep the escape hatch honest while it still exists:
  shell-mode routing, credential refusal, write verification, typed-first
  preference (`githubtypedfirst.go`), and per-phase exit gates.

Everything not yet expressible stays on a **guarded residual** path —
measurable, shipped-flag off, and gated — never silently dropped.

## 4. Phases and current state (as of this drive)

| Phase | Retires | State | Evidence anchor (in-repo) |
|---|---|---|---|
| 0. Measure | — (evidence foundation) | **SHIPPED** | classifier + `BashCoverageReport`, job `bash_retirement_report` |
| 1. GitHub CLI | `gh` category | **SHIPPED, measured** | `githubshell.go` bridge, `github_phase1` in job output |
| 2. Git | `git` category | **SHIPPED** | `gitshell.go` guarded bridge, `coder_git` typed family |
| 3. Shell modes | interactive/repair niche | OPEN | `shellmodes.go` routing |
| 4. Long tail | kubernetes/docker_sandbox/integration CLIs | OPEN | plan registry only |
| 5. Close | residual < threshold incl. credentials | OPEN | exit gates + criteria checklist |

Machine-checked gates: `retirementgate.go` (`BashRetirementPhaseGate`),
evidence blending `retirementevidence.go`, per-phase criteria checklist
`retirementcriteria.go`; the job emits `phases`, `criteria`,
`ready_to_close`, `criteria_ready_to_close` per run.

## 5. This drive’s scope (PR CI514 drive record)

- Publish this umbrella drive record.
- Add a machine-checked drive record in `backend/nativecoder`
  (`BashRetirementDriveRecord`) that states the target surface, the
  per-phase status and the phase that each category retires, plus a
  self-check that the classifier still recognises the primitive shapes the
  record claims are typed-covered — so the docs can never drift from the
  classifier that produces the metric.
- No overclaiming: categories without a shipped typed bridge stay
  `State: open` in the record even though the classifier can put their
  commands in a non-residual bucket (a bucket is *expressible*, not
  *shipped*; only `Shipped` categories count toward closing a phase).

## 6. Exit criteria for the umbrella

A phase closes only when, against a current transcript sample:

1. its share of the residual shell is below the plan threshold,
2. every declared criterion for the phase has evidence (criteria checklist),
3. No residual shape in the phase still has a shipped-and-flagged-on typed
   replacement — i.e. `ready_to_close` **and** `criteria_ready_to_close`.

The umbrella closes when every phase that retires a category is closed and
the residual shell is below the plan threshold — at which point `coder_bash`
is removed without loss of capability.

## 7. Reproduction

```sh
# Phase 0 + phase 1 + gates over a transcript export (stdin or file):
go run ./backend/scripts/bash_retirement_report < calls.jsonl          # JSON
go run ./backend/scripts/bash_retirement_report calls.jsonl --format=text
go run ./backend/scripts/bash_retirement_report --coder-prompt=prompt.txt calls.jsonl
go test ./backend/nativecoder/... ./backend/scripts/bash_retirement_report/...
```
