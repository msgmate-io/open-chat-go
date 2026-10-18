# ci#733 — Development-lead fleet-mode moderation toolkit + operating playbook

The native **development lead** runs the ci.msgmate.io coding fleet without an
operator watching every pass. ci#733 makes that pass mechanical, auditable and
safe: four small repo scripts plus this playbook.

Everything in this document is implemented in
[`development/scripts/devlead/`](../development/scripts/devlead/README.md) and
covered by `python3 -m unittest discover -s development/scripts/devlead`.

| Stage | Command | Read-only by default |
| --- | --- | --- |
| Audit | `devlead_fleet.py audit` | yes |
| Rescue | `devlead_fleet.py rescue` | yes (`--apply` to act) |
| Consolidate | `devlead_fleet.py consolidate` | yes (`--apply` to act) |
| Capacity | `devlead_fleet.py capacity` | yes |
| Label hygiene | `devlead_fleet.py label-audit` | yes (`--apply` to act) |

```bash
export OPEN_CHAT_API_TOKEN=...         # never written to disk, never printed
python3 development/scripts/devlead/devlead_fleet.py audit --json-out /tmp/audit.json
python3 development/scripts/devlead/devlead_fleet.py capacity --required 1
python3 development/scripts/devlead/devlead_fleet.py rescue --audit-file /tmp/audit.json        # dry-run
python3 development/scripts/devlead/devlead_fleet.py rescue --audit-file /tmp/audit.json --apply
python3 development/scripts/devlead/devlead_fleet.py consolidate                                # dry-run
python3 development/scripts/devlead/devlead_fleet.py label-audit
```

## 1. Audit the fleet

The audit lists every **managed** interaction with its tool-call count, edit
count and repeated-call count, then classifies it and recommends exactly one
action. It flags **thrash** when either threshold is crossed:

* `>= 30` tool calls with `0` edits — the agent is reading/looping, not changing
  code; or
* `>= 3` identical repeated calls — the same call is being re-issued, which
  wastes provider capacity without progress.

Counts come from the interaction view when the deployment reports them and fall
back to a cheap parse of the terminal text (`ran N tool calls`, `coder_edit` /
`coder_write` / `coder_patch` mentions), so the audit still works on a build that
only exposes prose.

| Classification | Trigger | Action |
| --- | --- | --- |
| `thrash` | ≥30 calls / 0 edits, or ≥3 repeated calls | `stop_and_rescue` (stop only if the turn is live), or report the blocker when the rescue budget is spent |
| `stalled` | `stall_no_first_turn` | one narrow `continue`; a repeated stall is re-dispatched with a narrower task instead |
| `finished_without_pr` | terminal `completed` / `no_usable_tool_call` / `turn_timeout`, no PR | one `continue` to commit/push or report what is missing |
| `pr_hygiene` | draft PR after a "done" declaration, or a PR `BEHIND` its base | undraft / update the branch |
| `ready_pr` | open, non-draft, mergeable, required context green | roll into the next consolidation (update the branch first if it is behind) |

Exit output ends with a machine-readable summary (`thrash`, `stalled`,
`finished_without_pr`, `ready_pr`, `rescue`, `by_action`), so a pass can be
journaled and compared between passes.

## 2. Rescue a flagged agent

A rescue is **stop then narrow continue**, in that order. The `stop` is sent
only when the turn is actually live — an idle thrash is continued directly,
because a stop on an idle interaction is a no-op that hides a wasted round trip.

The continue prompt is deliberately small (10-15 tool calls) and always asks for
a durable artefact:

```text
You are at 47 calls with ZERO edits. Implement the minimal change for ci#733 now:
add the provider-admission guard; commit/push a DRAFT PR; if blocked report the
exact blocker. Do not re-read files.
```

Guardrails:

* `resumes_per_chat` is capped at **2 per pass** (`DEFAULT_THRESHOLDS`); beyond
  that the audit recommends reporting the blocker rather than looping.
* `resume_auto_continue` is **off** for a rescue turn, so a chat that thrashed
  through unattended auto-continuation cannot restart that loop.
* A rescue never force-pushes and never merges.

## 3. PR hygiene

Behaviour: undraft PRs whose agent already declared done, update branches that
are `BEHIND` their base, and only ever merge on the **single required context**
— `Run PR CI Tests and Deployments` — never on an aggregate of optional checks.

### ci#683 REST fallback

The ruleset exposes one required context. When `gh pr merge` refuses because an
optional check is red (`review`/`authorization` gate on a non-required status),
fall back to the REST merge API, which only enforces required checks:

```bash
gh api -X PUT repos/msgmate-io/open-chat-go/pulls/<n>/merge \
  -f merge_method=squash
```

Never `--admin`-bypass and never force-push a consolidation branch. A red
**required** context always blocks the merge.

## 4. Consolidation drives

Exactly one consolidation batch is in flight at a time. The driver builds
`chore/consolidate-<date>-rNN` **from `main`** (never from a feature branch),
merges the ready feature branches with `--no-ff --no-edit`, bumps the version via
`development/scripts/bump_backend_version.sh`, pushes and opens the PR with the
full label set.

* `rNN` is `max(existing rN) + 1`, so the number keeps increasing even after a
  previous consolidation branch was closed unmerged.
* Previous `chore/consolidate-*` branches are never merged into a new batch.
* When a consolidation PR is still open, the plan returns
  `wait_for_open_consolidation` and defers the ready branches to the *next*
  batch — this keeps the runner queue and the review surface bounded.
* `consolidate` prints the full command list and refuses `--apply` unless the
  status is `ready`.

The PR is opened with the complete label set (`test build e2e sandbox-test
test-core-only ai-test-mock ai-test-real deploy`) because the consolidation is
the *only* place the heavy suites should run.

## 5. Capacity / CI-queue guard

Before dispatching, read `/api/v1/admin/provider-admission` and compare the
effective provider cap with the in-flight count:

```bash
python3 development/scripts/devlead/devlead_fleet.py capacity --required 1
```

* `allowed=false` (cap full, or cap unknown/`0`) ⇒ **do not dispatch**, wait for
  slots. Exit code is non-zero so a shell gate can use it directly
  (`--report-only` forces exit `0` for a pure report).
* A free slot with a non-empty queue is reported as such and limited to the
  number of free slots; never dispatch more than `cap - in_flight`.
* The same guard bounds consolidation/CI: cancel only *superseded* CI runs, never
  the run that is currently producing the consolidation or deploy gate.

## 6. CI discipline & label policy (Part G)

**Label policy — apply ONLY `test` and `build` (optionally `test-core-only`).**
Never apply `e2e`, `ai-test-mock`, `ai-test-real`, `sandbox-test` or `deploy` to a
feature/bugfix PR. The heavy suites live exclusively on the single
daily/`chore/consolidate-*` PR:

| PR kind | Labels |
| --- | --- |
| feature / bugfix | `test`, `build` (+ `test-core-only` when explicitly requested) |
| consolidation (one at a time) | `test`, `build`, `e2e`, `sandbox-test`, `test-core-only`, `ai-test-mock`, `ai-test-real`, `deploy` |

`label-audit --apply` removes any heavy label found on a feature PR and adds the
missing basic labels; it never adds `test-core-only` on its own. This is what
keeps a feature push from starving the consolidation/deploy pipeline in the
GitHub Actions runner queue.

Do not poll CI: run the fast targeted checks locally (touched Go package tests,
`gofmt`, touched frontend lint), open the PR early with the correct labels, and
let PR CI be the authoritative full-suite gate. Never cancel the in-flight
consolidation or deploy run.

## 7. The pass, end to end

1. **Audit** all managed agents, `--json-out` the snapshot.
2. **Capacity** — stop dispatching while the cap is full.
3. **Rescue** only `thrash` / `stalled` / `finished_without_pr`, max 2 per chat,
   dry-run first.
4. **PR hygiene** — undraft, update-behind, label-audit.
5. **Consolidate** exactly one `chore/consolidate-<date>-rNN`, merge only on the
   required green (ci#683 REST fallback), deploy, close absorbed tickets.
6. **Report** — active count, per-agent ticket/PR/calls/edits, rescue list,
   consolidation state, provider in-flight/queue, deployed version, tickets
   closed. Never poll CI.

## Relation to the deployed bot config

The compact **system-prompt instruction block** and the
`devlead_fleet_audit_thresholds` object live in
[`development/scripts/devlead/devlead_playbook.md`](../development/scripts/devlead/devlead_playbook.md);
this document is the long-form rationale. Merging the lead bot's config/prompt
with that file is an operator step (it touches the live bot config), so this
ticket ships the toolkit, the thresholds and the playbook text; the operator
applies them.
