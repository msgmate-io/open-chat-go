# ci#730 — Why is there so much custom coder logic in the public core?

Ticket: <https://github.com/msgmate-io/open-chat-go-ci/issues/730>
Scope owner: msgmate-coder integration (private), core = `open-chat-go` (public).

This document is the concrete inventory and the staged extraction plan required
before any large code movement. Slice 0 (this PR) is already implemented.

## Problem

`backend/nativecoder/` is a public-core package (~4.2k lines of production Go,
~6.5k with tests) that implements msgmate-coder's agent behaviour: CI-failure
routing, auto-consolidation planning, PR/GitHub side effects, branch checks,
tool-call budgets and coder interaction tags. The core imports it from seven
production files, so the public repo carries the private coding agent's business
logic, and two independently authored slices collided: ci#650 added
`tags.go`+`NormalizeRepo` while ci#608 added `devlead.go`+`NormalizeRepo`, so
`main` did not compile (`NormalizeRepo redeclared`, `go build ./nativecoder/`
fails). That collision is the symptom the ticket describes.

## Inventory (as of `28f849d`, VERSION 0.0.733)

Production files in `backend/nativecoder/` (LOC):

| File | LOC | Owns today | Should live |
| --- | --- | --- | --- |
| `capabilityplan.go` | 712 | Capability scenarios/plans for parity testing the coder | integration (as a parity harness) |
| `devlead.go` | 563 | `CIFailureEvent`, `DevLeadRouter`, lead-interaction routing | integration |
| `planner.go` | 461 | Auto-consolidation planner (`Plan`, PR selection, supersede) | integration |
| `github.go` | 430 | GitHub REST client, labels, rollup issue, merges, auto-approve | integration (needs Host HTTP/tokens) |
| `requiredchecks.go` | 412 | Required status checks / branch check states | integration |
| `policy.go` | 378 | Auto-consolidation policy resolution from config | integration |
| `toolbudget.go` | 365 | Tool-call budget snapshot/resolution/per-turn accounting | **core** (provider-agnostic) |
| `runner.go` | 306 | Consolidation run orchestration + branch prep | integration |
| `autonomous.go` | 191 | Coder autonomous-policy config (`coder_interaction`, `NormalizeCoderInteraction`) | integration, via a core hook |
| `agent.go` | 155 | `IsCoderAgentConfig`, config-default application, policy resolution | integration, via a core hook |
| `tags.go` | 98 | `DefaultInteractionTags`, `repo:`/`bot:` tag derivation | core-generic (tags) or integration hook |
| `repo.go` (new) | 76 | `NormalizeRepo`, `NormalizeBranch`, `slugify` | **core** (pure helpers) |
| `toolsconfig.go` | 53 | `DropEmptyTools` config hygiene | **core** (generic config hygiene) |
| `factory.go` | 47 | Consolidation client factory + test override | integration |

Tests in the same package: ~2.2k lines (`capabilityplan_test.go`,
`planner_test.go`, `devlead_test.go`, `runner_test.go`, `requiredchecks_test.go`,
`toolbudget_test.go`, `agent_test.go`, `autonomous_test.go`, `tags_test.go`,
`toolsconfig_test.go`, `repo_test.go`).

Core call sites (the coupling that has to shrink):

| Core file | Uses |
| --- | --- |
| `api/bots/Bots.go` | `DropEmptyTools`, `ApplyAutoConsolidationDefault`, `AutoConsolidationEnvValue`, `ApplyCoderAutonomousConfig`, `WithToolCallBudgetSnapshot`, `DefaultInteractionTags` |
| `api/chats/Create.go` | same config-default chain |
| `api/bots/AgentDispatch.go` | `ResolveAutonomousPolicy` |
| `api/bots/auto_consolidation.go` | `IsCoderAgentConfig`, `ResolvePolicyFromConfig`, `ConsolidationClientFactory`, `ConsolidationRunner`, `GitHubRepoFromEnv` |
| `api/msgmate/turn_outcome.go`, `provider_middleware.go` | `ToolCallBudget`, `ResolveToolCallBudget`, budget sources |
| tests | `auto_consolidation_test.go`, `turn_final_text_test.go`, `create_test.go`, `shared_config_tools_test.go` |

Direct coder literals (`msgmate_coder`, `coder_*`) do **not** leak into
`api/bots`, `api/chats`, `integrationhost`, `integrationsettings`,
`runtimecfg` or `chatstate` today — the coupling is entirely "core calls a
concrete coder package" rather than "core hardcodes coder strings". That makes
extraction tractable: it is an indirection problem, not a data-model problem.

Target layering:

* `integrationinterface` (already exists): Host capabilities + registries
  (`ChatBackends`, tools, config, HTTP, tokens, workspace).
* Missing surfaces the extraction needs (must be added to
  `go-integration-interface` first, one small PR each):
  1. `RegisterSharedConfigDefault(name, func(map[string]any) map[string]any)`
     — so the *core* applies defaults by draining a registry instead of calling
     `nativecoder.ApplyCoderAutonomousConfig` / `ApplyAutoConsolidationDefault`.
  2. `RegisterInteractionTagProvider(func(config, botName, explicit) []string)`
     — so `DefaultInteractionTags` becomes an integration-provided hook.
  3. `RegisterDispatchPolicy(func(config) DispatchPolicy)` and
     `RegisterAutoConsolidation(handler)` — so the core's dispatch and
     `api/bots/auto_consolidation.go` entry points delegate.
* Core keeps: `repo.go`, `toolsconfig.go`, `toolbudget.go` (pure, agent-agnostic),
  and thin hook call sites.
* Integration owns: devlead, planner, runner, policy, github, requiredchecks,
  capabilityplan, autonomous/agent config, tags, factory.

## Plan (small, safe slices)

**Slice 0 — repair `main` (this PR, no behaviour change).**
`main` is broken; two `NormalizeRepo`/test pairs exist. Deduplicate into one
canonical `repo.go` implementation (the stricter ci#650 URL/scp-aware variant),
one canonical test, and leave pointers where the duplicates were. No semantic
change for the devlead tests: the ci#650 variant normalises everything the ci#608
variant did (case, `.git`, surrounding space) for `owner/name` inputs.

**Slice 1 — core-generic helpers move out of the coder namespace.**
Move `toolbudget.go` to `backend/toolbudget` (or `backend/agentruntime`) and
`DropEmptyTools` to a config utility; update the ~4 core call sites. Pure move,
tests move with the code. No new interface needed.

**Slice 2 — config defaults through a registry.**
Add the `integrationinterface` shared-config-default registry, port
`ApplyCoderAutonomousConfig`, `ApplyAutoConsolidationDefault` and
`DropEmptyTools` wiring to it, and reduce `api/bots/Bots.go` / `api/chats/Create.go`
to "drain registry, then apply defaults". The coder implementations move to the
integration in the same step.

**Slice 3 — interaction tags via a provider hook.**
Add the tag-provider registry, register `DefaultInteractionTags` from the
integration, and delete `tags.go` from core.

**Slice 4 — auto-consolidation + devlead behind one Host entry point.**
Add the dispatch/auto-consolidation registry; move `policy.go`, `planner.go`,
`runner.go`, `github.go`, `requiredchecks.go`, `devlead.go` and their tests into
the integration, keeping in core only a generic "run the registered
auto-consolidation handler for this bot config" call. This is the bulk of the
~2.6k lines and should be its own PR pair (interface + move).

**Slice 5 — capability parity harness.**
Move `capabilityplan.go` into the integration as the parity suite it exists for;
keep the core-side test that consumes the plan only if it can run without the
coder scenario definitions.

**Slice 6 — guard.**
Add a cheap CI/architecture guard so the core cannot re-absorb coder logic:
a test asserting no package outside `backend/nativecoder` (or its successor
package) imports it, plus a note in `AGENTS.md` / `DEVELOPMENT.md` that coder
behaviour belongs to the integration and core only exposes generic hooks.

Ordering rationale: each slice is independently mergeable and green on its own;
the registry slices (2–4) need the interface module published first, so they are
sequenced after the pure-move slices (0–1) that need no interface change.

## Status

| Slice | State |
| --- | --- |
| 0 — dedupe `NormalizeRepo`, repair `go build ./nativecoder/` | implemented in this PR |
| 1 — move agent-agnostic helpers to core packages | planned |
| 2 — shared-config-default registry | planned (needs interface PR) |
| 3 — interaction-tag provider registry | planned (needs interface PR) |
| 4 — consolidation/devlead behind Host entry point | planned (needs interface PR) |
| 5 — capability parity harness into the integration | planned |
| 6 — architecture guard | planned |
