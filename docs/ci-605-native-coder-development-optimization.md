# ci#605 — Optimizing how the native coder develops open-chat-go

Status: investigation complete (this document), plus one small code change
(F7, frontend tool-row coverage). Follow-ups are listed in §5 and tracked by a
single umbrella ticket (see §5, `T0`).

Scope: how the *native* coder agent (`msgmate_coder*`, the autonomous
auto-sandbox mode described in `AGENTS.md` / `DEVELOPMENT.md`) actually gets work
done in this repo, measured from a real end-to-end run of a CI ticket, and which
changes remove the biggest wastes.

Everything below is evidence-based: metrics come from the tool-result accounting
of the run itself (budget used, results truncated, results evicted). Where a
claim is a hypothesis it is marked as such.

## 1. The run under the microscope

The reference run implemented this very ticket:

| Metric | Value |
|---|---|
| Context budget at the end | ~86 % used of a 120 000-char budget (~26 k tokens) |
| Tool results truncated | 10 |
| Tool results evicted from context | 7 |
| Redundant re-runs of an identical command | 3 (`gh issue view` → file → failed read) |
| Wall-clock spent on discovery vs. editing | ≳ 60 % / ≲ 15 % (tool-call count ratio) |

The important number is **5 of 5 deliberately multi-command batches were
truncated** (the per-tool output budget cuts a batch at ~8 000 chars while the
command returned 8.6 k–12.5 k). The workarounds — re-running a *narrower*
command, or redirecting to a file and slicing it — are all extra round trips.

## 2. Findings, ranked by cost per run

### F1 — Scratch files are outside the readable workspace (high)

`coder_bash` happily writes `/tmp/x`, but `coder_read` rejects it:

```
Tool coder_read failed with error (400): path is outside the workspace root.
```

The natural "dump, then read a range" pattern therefore *cannot* be completed
with the file the shell can write. In this run that cost **three** redundant
tool calls (`gh issue view > /tmp/605.json` three times, then three failed
reads, then the same content dumped again through `sed`).

Fix direction: allow reads of a per-chat scratch directory (e.g. the workspace's
`.coder-scratch/`), or explicitly permit `/tmp` for reads. Small, tool-side.

### F2 — A batch shares one output budget (high)

`coder_bash` with `commands: [...]` is the recommended way to save approvals,
but the whole batch shares one ~8 000-char budget and the cut is **silent for
the tail**: when the batch returned 10 967 chars, the last two commands were
not in context at all, and nothing in the result said "command 4 of 5 omitted".

Fix direction: per-command truncation with an explicit marker (`truncated`,
`omitted_commands`), or middle-slicing (`head` + `tail` + a count) so a batch's
*conclusions* survive. Small, tool-side. Wasted round trips: ~2 per run.

### F3 — There is no typed GitHub *issue* reader (high)

The repo already ships typed GitHub tools (`gh_code_review`, `gh_pr_comment`,
`repo_changes_summary`, Actions runs — parity doc §3.1). For issues there is only
`gh issue view --json body,comments`, which returns multi-kilobyte bodies with
embedded logs. For an issue-driven agent this is the single most frequently
executed command in the whole session, and its result is the most likely to be
truncated (the issue body *and* the comments are needed, in order, both).

Fix direction: mirror `coder_gh_issue` (`body_limit`, `comments: n|all`,
`comments_limit`) returning a compact structured result. Small, tool-side;
largest expected call-count saving for this repo's workflow.

### F4 — The playbook the coder needs is fragmented across a 3.5 k-line workflow (medium)

`AGENTS.md` and `DEVELOPMENT.md` are byte-identical (`cmp` → identical), so the
coder reads the same content twice if it reads both. The task-relevant rules —
PR labels and their policy, the pin-freshness gate, the frozen-workspace build,
how to run the frontend unit tests — live inside
`.github/workflows/run-pr-ci-tests-and-deployments.yaml`, which is ~3 500 lines;
finding one rule costs several greps plus range reads.

Fix direction: a short "task recipes" section in `AGENTS.md` — one numbered,
copy-pasteable command sequence per task class (open a draft PR, run one
package's tests, run the pin gate, bump a lock). Documentation-only, no behaviour
risk. Also: make one of the two files a symlink or a one-line pointer.

### F5 — Sandbox provisioning does not match the tests the coder is asked to run (medium)

* `go version` → `not found` (no Go toolchain), and
* no `frontend/node_modules` (no vitest).

So a coder instructed to "run targeted tests" can, in the default
auto-sandbox, run *nothing* until it restores dependencies. In this run the
verification had to be delegated to PR CI, which is exactly the latency the
testing policy tries to avoid for routine changes.

Fix direction: bake Go (version from `backend/go.mod`) and a warm
`frontend/node_modules` into the sandbox image, or make `coder_env` assert the
toolchain and fail the *provisioning* step loudly instead of the first test
command. Also worth checking: whether the repo snapshot in the session matches
the one named by `auto_sandbox`.

### F6 — The budget guard is visible but not actionable (low/medium)

The session prompt reports the context budget, and (per the parity doc)
`coder_context` / `coder_compact` exist as tools — but in the native auto-sandbox
toolset they were **not exposed** in this run, and a truncated result carries no
"original size" hint, so the coder cannot tell a 2 k cut from a 40 k cut without
re-reading.

Fix direction: expose the usage report (`coder_context`) in the default toolset
and stamp every truncated result with `original_bytes` + how to narrow the query.
Small, tool-side.

### F7 — The documented parity tool surface renders as generic rows (low, **fixed here**)

`docs/native-coder-parity.md` §3.1 lists `coder_codesearch`, `coder_context` /
`coder_compact`, `coder_todo_*`, `coder_task` and `coder_env` as closed gaps,
but the shared frontend helpers that render tool-call rows
(`frontend/packages/ui/src/integration/components/chat/toolCallSummary.ts`) had
no label, category, parameter summary or result preview for any of them: every
one of them collapsed to a bare "Tool" row with no detail. That makes a
transcript *harder* to audit precisely where the new autonomy is concentrated
(context compaction and sub-agents spend the most tokens).

Fixed in this PR (§3).

### Non-findings (checked, deliberately unchanged)

* The `no workspace selected for this chat; call coder_select_workspace first`
  contract is asserted by tests (`backend/api/tools/confirmable_actions_test.go`,
  `backend/api/msgmate/tool_failure_retry_test.go`) and is good guidance; do not
  soften it.
* CI label policy (`deploy` / `sandbox-test` only on consolidation PRs) is
  correct and should keep the heavy suites off feature PRs.
* `check_pin_freshness.py` / `check_frozen_workspace.py` gates are cheap and
  deterministic; no change proposed.

## 3. Code change in this PR

`frontend/packages/ui/src/integration/components/chat/toolCallSummary.ts`
(+ its unit test): the parity tool surface now gets real rows.

* categories: `codesearch`, `context`, `compact`, `todo_read`, `todo_write`,
  `env`, `task` → `read` (so the collapsed group summary counts them);
* labels: `Code search`, `Context`, `Compact`, `Todo`, `Sub-agent`,
  `Environment`;
* parameter summaries: the search pattern + kind + scope, the context query (or
  `context usage`), the compaction focus, the todo count, the probed variable,
  the sub-agent name + truncated prompt;
* result previews: match count, `tokens_used/token_budget`, tokens saved /
  messages compacted, todo count, sub-agent status.

Tests: `frontend/packages/ui` unit project
(`npm test` → `vitest --project unit run`); the new cases are in
`toolCallSummary.test.ts`. No behaviour outside the row rendering changes.

## 4. What "optimized development" should look like

A measurable target for the next run of this size:

| Signal | Today | Target |
|---|---|---|
| Tool results truncated | 10 | ≤ 3 |
| Redundant re-runs of one command | 3 | 0 |
| Local test execution possible? | no | yes (targeted package) |
| Discovery calls before first edit | ≳ 20 | ≤ 10 |

## 5. Follow-up tickets

`T0` — **umbrella follow-up for ci#605**:
https://github.com/msgmate-io/open-chat-go-ci/issues/731 — holding the concrete,
small items below, each of which is independently mergeable:

1. `T1` typed `coder_gh_issue` reader with body/comment limits (F3).
2. `T2` per-command truncation markers + middle-slicing for `coder_bash`
   batches (F2).
3. `T3` scratch-space reads: a readable in-workspace scratch dir (F1).
4. `T4` sandbox image parity: Go toolchain + warm `node_modules`, asserted by
   `coder_env` at provisioning time (F5).
5. `T5` `AGENTS.md` task recipes (+ de-duplicate `DEVELOPMENT.md`) (F4).
6. `T6` expose `coder_context` in the default toolset and stamp truncated
   results with their original size (F6).

Per ticket hygiene these are folded into `T0` rather than filed as six separate
issues; `T1`/`T2`/`T3` are the highest value and each is a single small tool-side
change.
