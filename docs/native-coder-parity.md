# Native coder parity sign-off (vs. the opencode bot suite)

Status: **signed off — GO** for the per-bot `opencode` → `msgmate_coder` flip on
`ci.msgmate.io` (tracker: `msgmate-io/open-chat-go-ci#581`, flip itself: `#582`).
Signed off at backend `VERSION 0.0.712` (consolidation r20, open-chat-go#671 /
merge commit `197b622`) on 2026-10-17.

This document records **what was verified**, **how it was verified** and the
**explicitly accepted fallbacks**. It contains no secret material: no tokens,
kubeconfigs or configuration contents — only repository names, public test
names and their outcomes.

## 1. Scope

The native `msgmate_coder` coding agents must reach capability parity with the
`opencode` integration bots they replace, so the `ci.msgmate.io` development
bots can be flipped one at a time. The two blocking questions were:

1. Does every opencode bot role have a native counterpart with the same *tool
   surface* (exploration, edits, search, shell, git, GitHub, MCP, sub-agents,
   context management)?
2. Does a native agent actually complete a real ticket end to end
   (plan → implement → PR) on the deployment, not just in unit tests?

Both are answered below. The formal acceptance items of `ci#581` were:

- a written parity matrix (this document);
- every gap either closed or accepted with a fallback that keeps `opencode` for
  that role;
- one end-to-end run of a native agent on a real ticket.

## 2. Bot-by-bot mapping

| opencode bot | role | native counterpart | status |
|---|---|---|---|
| `issue-plan-bot` | investigate an issue and post a plan | `msgmate-coder-plan-bot` | **parity** — native agent, typed GitHub reads, read-only plan mode |
| `issue-repl-bot` | implement and open a PR | `msgmate-coder-repl-bot` | **parity, proven end to end** (open-chat-go#672, §4) |
| `coding-bot` | frontend/feature implementation work | `msgmate-coder-repl-bot` | **parity** — same surface, plus typed build/test/lint/run tools |
| `open-chat-go-ci-admin` | server/dev-ops, kubeconfig workflows | native admin/lead route | **accepted gap** — see §3.2; `opencode` stays the fallback for this role |
| `open-chat-development-lead` | triage, dispatch, consolidation | `msgmate-coder-development-lead` | **parity** — native agent + hive-mind dispatch (`ci#540`) |

## 3. Capability matrix

### 3.1 Closed gaps (each with a merged implementation and tests)

| Capability | Native implementation | Evidence |
|---|---|---|
| Exploration: `read` / `list` / `glob` / `grep` | `coder_*` core tools (structured reads incl. images/PDF/notebooks) | `msgmate_coder_*` suite, `coder_structured_read` |
| Multi-hunk / multi-file edits | `coder_patch` | `msgmate_coder_patch` |
| Code search / symbol navigation | `coder_codesearch` | `msgmate_coder_codesearch` |
| Background, streaming and interactive shell | background bash + stdin + shell mode | `msgmate_coder_bash_stream`, `_shell_mode` |
| Todo / task list | `coder_todo_write` / `coder_todo_read` | `msgmate_coder_todo` |
| Sub-agents | `coder_task` (fresh context, restricted tools) | `msgmate_coder_task`; hardening in msgmate-coder#66 |
| MCP tools (chat-scoped, remote) | chat `mcp:*` tools in the core loop | `msgmate_coder_openchat_mcp` |
| Per-workspace / sub-agent MCP | `CallMCPTool` export (mcp integration #9) + `coder_task` MCP scoping (msgmate-coder#75); pinned in open-chat-go#670 | unit tests on both sides; mcp pin `9a90a95` |
| Hive-mind inter-bot | hive-mind tools + agent dispatch | `msgmate_coder_hive_mind` (`ci#540`) |
| Session trust / autonomous no-confirm | `coder_interaction: autonomous` | `msgmate_coder_command_confirmation`; dispatch default `ci#532` (#587) |
| Workspace / sandbox provisioning | sandbox integration, per-chat auto-sandbox | `msgmate_coder_in_sandbox` (`ci#413`); capacity fix `ci#602` (#604) |
| Web research | `coder_websearch` / `coder_webfetch` | `msgmate_coder_*` |
| Git read + write (+ plumbing) | `coder_git`, confirmation-free `coder_git_read`, plumbing (`ls_files` / `rev_parse` / `cat_file`) | msgmate-coder#61, #71 |
| Typed GitHub tools | review + PR comment + repo summary + Actions runs | `msgmate_coder_gh_code_review`, `_gh_pr_comment`, `_repo_changes_summary`; msgmate-coder#59/#65/#70 |
| Context compaction | `coder_context` / `coder_compact` | `msgmate_coder_compact`; robustness in msgmate-coder#68; core-side context budgeting `ci#680` (per-tool output budget, conversation budget guard, `coder_context` usage report) |
| Typed install / build / test / lint / run | typed task tools | msgmate-coder#63 |
| Environment probe | `coder_env` | msgmate-coder#74 |
| Shell-surface coverage classification | `coder_bash` coverage classes (`ci#514` phase 0) | msgmate-coder#72 |
| Guarded `coder_bash` allow-list | per-chat allow-list (`ci#514` phase 5, first step) | msgmate-coder#73 |

All nine gaps originally tracked in `msgmate-io/open-chat-go-ci#394` now have a
merged implementation; the umbrella stays open only for the follow-ups in §3.2
(and for the migration itself).

### 3.2 Accepted fallbacks (deliberate, no capability loss)

1. **Typed `kubernetes` / `docker_sandbox` / `ssh` / `matrix` CLIs** are not yet
   typed (`ci#514` phase 4; the exposure path is the `ci#423` Host boundary).
   *Fallback:* `coder_bash` plus each integration's own tools — the same
   actions, at a per-call confirmation cost. This is why the
   `open-chat-go-ci-admin` role keeps `opencode` until phase 4 lands.
2. **stdio MCP servers** are not launched by the native tool loop; only remote
   (HTTP-streamable) MCP servers are supported. A stdio-only preset is reported
   as unsupported instead of silently failing. *Fallback:* keep `opencode` for a
   role that hard-requires a stdio preset.
3. **Shared/pooled sandbox mode is staged, not enabled.** `ci#604` supplies the
   right-sized requests (coder *minimal* template at a `250m` CPU request; the
   `4` CPU limit unchanged), which restores capacity on the single 6-vCPU node;
   its bounded pooled-mode delta (max agents per pool, idle TTL) stays staged
   until the per-agent isolation contract of `ci#603` lands, so native chats
   keep their own pod for now.
4. **`opencode` remains installed and is the default/backup route.** The flip is
   per bot and reversible through the `ci#582` rollback delta, so accepting a
   fallback never strands a role.

## 4. End-to-end evidence

A native agent completed a real ticket on the deployment:

- Ticket: `msgmate-io/open-chat-go-ci#569` (tool-call widgets / UI follow-up).
- The native `msgmate-coder-repl-bot` investigated, implemented and **opened
  open-chat-go#672** (`feat/ci-569-turn-timeline`) — plan → implement → PR, with
  the ordinary CI labels applied by the agent itself. The change spans the
  backend (`ToolCall` timing, the ordered turn timeline, streaming) and the
  shared UI package (11 files, ~1 042 insertions) and came with backend
  (`turn_timeline_test.go`) and UI (`turnTimeline.test.ts`, timing/spinner
  cases) tests. It was folded into the r20 consolidation open-chat-go#671 and
  released as `0.0.712`.

Post-deploy sandbox benchmark on the r20 deployment (PR open-chat-go#671,
"Docker Sandbox Benchmark Summary", 2026-10-17 19:15 UTC) — the native coder
tests green:

| Benchmark test | Result |
|---|---|
| `msgmate_coder_gh_code_review` | ✅ passed |
| `msgmate_coder_gh_pr_comment` | ✅ passed |
| `msgmate_coder_repo_changes_summary` | ✅ passed |
| `msgmate_coder_fibonacci_json` | ✅ passed |
| `playwright_mcp_*`, `opencode_openchat_mcp` | ✅ passed (MCP surface) |

The extended private suite (`private-sandbox-tests.yaml`, `tags=coder`) run on
the same deployment was green apart from one `msgmate_coder_repo_changes_summary`
failure (`39064349284`), which is the known model-side flakiness tracked in
`ci#510` / `ci#562` — the same assertion passes in the public benchmark above and
in the earlier private run `39032667058`. It is not a capability gap; a fresh
private-suite run on the r20 merge commit was still in flight when this sign-off
was written.

Sandbox provisioning was the last infrastructure blocker (`ci#602`: every native
coder chat requested a fresh pod at the deployment-wide `500m` request while
finished chats pinned CPU for their whole TTL, so new pods stayed `Pending` with
`0/1 nodes are available: 1 Insufficient cpu`). It is fixed by the Sandbox
Resource Reaper apply run (`39064729037`, which reclaimed the stale per-chat
coder pods ≈3 CPU) together with the right-sized requests of `ci#604`; the node
now sits at ~49% CPU requests with no Pending pods, and this sign-off run itself
provisioned a native per-chat sandbox successfully (the previous attempt had
failed on CPU exhaustion).

## 5. Verdict

- **Bots:** `msgmate-coder-plan-bot`, `msgmate-coder-repl-bot` (covers
  `coding-bot`) and `msgmate-coder-development-lead` are at parity → flip first.
- **`open-chat-go-ci-admin`:** parity not yet claimed; keep `opencode` for that
  role until `ci#514` phase 4.
- **Capabilities:** all nine `ci#394` gaps closed; four fallbacks accepted
  (§3.2), none of which blocks a coding role.
- **End-to-end:** demonstrated with open-chat-go#672 (§4).
- Therefore the **`ci#582` flip may proceed** per bot (native lead last), with
  the rollback delta available and the admin role held back.

## 6. Where the evidence lives

- Public sandbox benchmark: `.github/workflows/sandbox-benchmark.yaml` /
  `reusable-sandbox-benchmark.yaml` (run in PR CI with the `sandbox-test`
  label); results are posted back as a PR comment.
- Extended native-coder suite: the private CI repository's
  `private-sandbox-tests.yaml` (`tags=coder`), run after a deployment.
- Pinned integration surface: `integrations.lock.json` (public) plus the private
  lock for private integrations; the parity work shipped as msgmate-coder
  #59–#75 and mcp integration #9.
- Trackers: `ci#581` (this sign-off), `ci#394` (parity gaps), `ci#582` (flip),
  `ci#602` / `ci#603` / `ci#604` (sandbox capacity and isolation).
