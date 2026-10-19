# development/scripts/devlead — ci#733 fleet-moderation toolkit

Small, dependency-free (`python3` stdlib only) scripts that make the native
**development lead** pass mechanical, auditable and safe. Long-form guide:
[`docs/ci-733-devlead-fleet-moderation-playbook.md`](../../../docs/ci-733-devlead-fleet-moderation-playbook.md).

| File | Purpose |
| --- | --- |
| `devlead_fleet.py` | `audit`, `rescue`, `consolidate`, `capacity`, `label-audit` |
| `test_devlead_fleet.py` | unit tests for every pure decision helper + CLI wiring |
| `devlead_pass.sh` | one-shot pass wrapper (audit -> capacity -> rescue -> label-audit -> consolidate), dry-run by default |
| `devlead_playbook.md` | system-prompt instruction block + `devlead_fleet_audit_thresholds` for the lead bot config |

## Quick start

```bash
export OPEN_CHAT_API_TOKEN=...   # or CI_API_TOKEN; never written to disk, never printed

python3 -m unittest discover -s development/scripts/devlead -v   # tests
bash development/scripts/devlead/devlead_pass.sh                 # dry-run pass
bash development/scripts/devlead/devlead_pass.sh --apply --yes   # act
```

## The five subcommands

```bash
devlead_fleet.py audit       [--from-file snapshot.json] [--json-out out.json]
                             [--max-calls 30] [--max-repeats 3]
devlead_fleet.py rescue      --audit-file out.json [--chat UUID] [--step '...']
                             [--apply] [--resume-auto-continue]
devlead_fleet.py capacity    [--from-file admission.json] [--required 1] [--report-only]
devlead_fleet.py consolidate [--from-file prs.json] [--today YYYY-MM-DD] [--apply]
devlead_fleet.py label-audit [--from-file prs.json] [--apply]
```

All subcommands are **read-only unless `--apply`** is given, and every mutating
action is printed before it is sent. `audit`/`rescue`/`capacity` read live data
from `OPEN_CHAT_HOST` (default `https://ci.msgmate.io`); `consolidate` and
`label-audit` read PRs through `gh`. `--from-file` accepts a captured JSON
snapshot instead, which is what the unit tests and offline reviews use.

### Flagging rules (audit)

A managed interaction is **thrash** when

* it used `>= 30` tool calls with `0` edits, or
* it issued `>= 3` identical repeated calls.

Tool-call / edit / repeated-call counts come from the interaction view and fall
back to parsing the terminal text, so the audit still flags thrash on a
deployment that only reports prose. Other classifications are `stalled`
(`stall_no_first_turn`), `finished_without_pr` (terminal
`completed`/`no_usable_tool_call`/`turn_timeout` with no PR), `pr_hygiene`
(draft after a done declaration, or `BEHIND` base) and `ready_pr` (open,
non-draft, mergeable, required context green).

### Rescue ordering

`rescue` **stops first, then continues** with a 10-15 call prompt that asks for a
DRAFT PR or an exact blocker. The stop is skipped when the turn is already idle,
`resume_auto_continue` is off by default, and a chat is rescued at most twice per
pass — after that the audit recommends reporting the blocker instead of looping.

### Capacity

`capacity` reads `/api/v1/admin/provider-admission` and refuses dispatch when
`cap - in_flight < required` (or when the cap is unknown/`0`). It exits non-zero
in that case so a shell gate can use it directly.

### Label policy (ci#733 Part G)

Feature/bugfix PRs carry **only** `test` and `build` (optionally
`test-core-only`). `e2e`, `ai-test-mock`, `ai-test-real`, `sandbox-test` and
`deploy` belong exclusively on the single `chore/consolidate-<date>-rNN` PR;
`label-audit --apply` strips them from feature PRs.

## Developing

```bash
python3 -m unittest discover -s development/scripts/devlead -v
python3 -m py_compile development/scripts/devlead/devlead_fleet.py
```

Keep the decision logic in the pure helpers (`classify`, `audit`,
`render_rescue_prompt`, `rescue_actions`, `capacity_decision`, `label_actions`,
`plan_consolidation`) so it stays testable without a live API or `gh`.
