package nativecoder

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the machine-readable form of the native coder capability test
// plan (ci#390): a registry of real-world scenarios that exercise the msgmate
// coder end to end on a deployed server (ci.msgmate.io), each with the tools it
// is expected to use, the capabilities it covers and the invariants a run must
// satisfy.
//
// The registry is deliberately data-only: it is the single source of truth that
// (a) the in-repo unit tests validate, (b) the live harness (private
// `trial-msgmate-coder-scenarios.yaml` / selfcheck suites in the CI repository)
// mirrors, and (c) the human-facing plan in
// `docs/native-coder-capability-plan.md` documents. Adding a capability to the
// plan means adding a scenario here; ValidateCapabilityPlan then fails until
// every core capability is covered.
//
// No secrets, tokens or deployment configuration are part of this plan: it
// contains scenario prompts, tool names and assertion descriptions only.

// CapabilityPlanVersion is the plan schema version. The live harness reports it
// with every run so evidence can be attributed to a plan revision.
const CapabilityPlanVersion = 1

// capabilityMarkerToken is the placeholder a scenario prompt must contain. The
// harness replaces it with a per-run nonce so a final reply can only satisfy the
// marker invariant by actually running the scenario (a stale or hallucinated
// reply cannot contain a fresh nonce).
const capabilityMarkerToken = "{{MARKER}}"

// CapabilityTierCore marks a capability that must be covered by at least one
// scenario for the plan to validate.
const CapabilityTierCore = "core"

// CapabilityTierExtended marks a capability that is tracked but not required
// for the plan to be considered complete (heavy or deployment-specific).
const CapabilityTierExtended = "extended"

// CapabilityClass is one capability of the native coder that the plan must
// exercise.
type CapabilityClass struct {
	Name        string `json:"name"`
	Tier        string `json:"tier"`
	Description string `json:"description"`
}

// CapabilityScenario is one real-world, end-to-end scenario of the plan.
type CapabilityScenario struct {
	// ID is the stable scenario identifier used by the harness and the docs.
	ID string `json:"id"`
	// Title is a one-line human description.
	Title string `json:"title"`
	// Role is the native coder role under test (lead, repl, review, plan, admin).
	Role string `json:"role"`
	// Capability is the capability class this scenario is the primary evidence for.
	Capability string `json:"capability"`
	// Tags select the scenario in a harness run (eg live, sandbox, mutating).
	Tags []string `json:"tags"`
	// RequiresSandbox reports whether the scenario needs a bound coding sandbox.
	RequiresSandbox bool `json:"requires_sandbox"`
	// RequiresLiveWeb reports whether the scenario needs outbound internet.
	RequiresLiveWeb bool `json:"requires_live_web"`
	// Mutating reports whether the scenario changes the workspace, a repo or the
	// platform (a mutating scenario must run in a disposable sandbox/chat).
	Mutating bool `json:"mutating"`
	// Tools are the tool names the scenario is expected to exercise.
	Tools []string `json:"tools"`
	// Prompt is the task message handed to the agent. It must carry the marker
	// token so the harness can assert on a fresh nonce.
	Prompt string `json:"prompt"`
	// Acceptance are the invariants the harness asserts (in addition to "the run
	// finished without an error" and "the final reply contains the marker").
	Acceptance []string `json:"acceptance"`
	// Tracks are the ci ticket numbers this scenario is the regression evidence
	// for.
	Tracks []int `json:"tracks"`
}

// CapabilityTestPlan is the whole plan: its schema version, the capability classes
// and the scenario registry.
type CapabilityTestPlan struct {
	Version   int                  `json:"version"`
	Classes   []CapabilityClass    `json:"classes"`
	Scenarios []CapabilityScenario `json:"scenarios"`
}

// capabilityClasses is the capability inventory of the plan. Every core class
// must be covered by at least one scenario.
var capabilityClasses = []CapabilityClass{
	{"web_research", CapabilityTierCore, "live web fetch/search with source-grounded answers"},
	{"sandbox_provisioning", CapabilityTierCore, "per-chat auto sandbox provisioning and project scaffolding"},
	{"workspace_continuity", CapabilityTierCore, "the chat keeps one workspace across turns and re-attaches to it"},
	{"edit_and_git", CapabilityTierCore, "read/edit/patch plus git status/diff/commit plumbing"},
	{"search_semantics", CapabilityTierCore, "coder_list/glob/grep semantics and path scoping"},
	{"confirmation_policy", CapabilityTierCore, "the confirmation gate approves, rejects and never retries a rejected action"},
	{"autonomy", CapabilityTierCore, "trust=allow autonomous runs complete without parking on a confirmation"},
	{"auto_continue", CapabilityTierCore, "incomplete turns are retried so work reaches a branch/PR"},
	{"typed_github", CapabilityTierCore, "typed GitHub tools (issue/PR read + verifiable writes)"},
	{"plan_mode", CapabilityTierCore, "plan mode stays read-only yet can publish its plan"},
	{"context_budget", CapabilityTierCore, "large tool outputs are budgeted instead of overflowing the context"},
	{"tool_budget", CapabilityTierCore, "per-chat tool budgets cap runaway turns without losing the final summary"},
	{"subagents", CapabilityTierCore, "coder_task sub-agents run in a fresh, restricted context"},
	{"shell_modes", CapabilityTierCore, "foreground, background/streaming and interactive shell modes"},
	{"hive_mind", CapabilityTierCore, "cross-agent dispatch and observation through the hive mind"},
	{"error_recovery", CapabilityTierCore, "tool/infra failures surface as errors and the agent recovers"},
	{"observability", CapabilityTierCore, "tool calls and reasoning stay visible in the chat and its metadata"},
	{"consolidation", CapabilityTierExtended, "ready PRs are consolidated and merged by the native coder"},
	{"emergency_recovery", CapabilityTierExtended, "a restricted second-deployment bot monitors, diagnoses and recovers a crashed server"},
	{"parallelism", CapabilityTierExtended, "independent concurrent sessions stay isolated on pooled sandboxes"},
}

// capabilityScenarios is the scenario registry of the plan.
var capabilityScenarios = []CapabilityScenario{
	{
		ID:              "web_research_live",
		Title:           "Answer from two live public pages and cite them",
		Role:            "repl",
		Capability:      "web_research",
		Tags:            []string{"live", "no-sandbox"},
		RequiresLiveWeb: true,
		Tools:           []string{"coder_webfetch", "coder_websearch"},
		Prompt: "Real-world capability scenario WEB RESEARCH. Fetch at least two different public URLs " +
			"(use coder_webfetch, and coder_websearch if it is available), then answer in one short paragraph: " +
			"state the exact title of the first page and the source URL of every fact. If a web tool errors, " +
			"report the verbatim error instead of inventing data. Do not modify any file. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the reply contains the marker nonce",
			"at least one coder_webfetch/coder_websearch tool call succeeded (no tool error)",
			"the reply cites at least two distinct http(s) URLs",
			"a web-tool failure is reported verbatim instead of being papered over",
		},
		Tracks: []int{390, 427, 451},
	},
	{
		ID:              "sandbox_project_scaffold",
		Title:           "Provision a sandbox, scaffold a project and run it",
		Role:            "repl",
		Capability:      "sandbox_provisioning",
		Tags:            []string{"live", "sandbox", "mutating"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"coder_select_workspace", "coder_write", "coder_bash"},
		Prompt: "Real-world capability scenario SANDBOX PROJECT SETUP. Ensure you are bound to a coding workspace " +
			"(select an auto sandbox workspace if none is bound). Then scaffold a tiny project: create a directory " +
			"cap_scenario and write cap_scenario/main.py that prints exactly HELLO_FROM_CAP, and run it with " +
			"coder_bash (python3 cap_scenario/main.py). Report the exact stdout and the path you used. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the reply contains the marker nonce",
			"a workspace was bound (coder_select_workspace succeeded) before the first write",
			"the reported stdout contains HELLO_FROM_CAP",
			"no tool call failed with a sandbox provisioning error",
		},
		Tracks: []int{390, 602, 666, 688},
	},
	{
		ID:              "workspace_continuity_multiturn",
		Title:           "Keep one workspace across turns without re-selecting it",
		Role:            "repl",
		Capability:      "workspace_continuity",
		Tags:            []string{"live", "sandbox", "multiturn"},
		RequiresSandbox: true,
		Tools:           []string{"coder_select_workspace", "coder_bash", "coder_list"},
		Prompt: "Real-world capability scenario WORKSPACE CONTINUITY. Turn 1: write the file cap_persist.txt " +
			"containing the marker " + capabilityMarkerToken + ". Turn 2 (a later message in this same chat): " +
			"without selecting a new workspace, read cap_persist.txt back with coder_read and print its content. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the second turn reads the file written in the first turn from the same workspace",
			"the chat does not re-provision a second sandbox for the continuation",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 399, 526},
	},
	{
		ID:              "multistep_edit_git",
		Title:           "Read, create, edit and inspect with git",
		Role:            "repl",
		Capability:      "edit_and_git",
		Tags:            []string{"live", "sandbox", "mutating"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"coder_read", "coder_write", "coder_edit", "coder_bash", "coder_git"},
		Prompt: "Real-world capability scenario MULTI-STEP EDIT + BASH + GIT. In the bound workspace: (1) read the " +
			"top-level README.md, (2) create cap_multistep.txt whose only line is CAP_MULTISTEP_EDIT, (3) edit it so " +
			"the line becomes CAP_MULTISTEP_EDITED, (4) run `git status --porcelain` and `git --no-pager diff --stat` " +
			"with coder_bash, (5) summarize what changed and report the verbatim git output. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the file exists with the edited content (CAP_MULTISTEP_EDITED)",
			"the reported git output is the verbatim command output",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 514, 630},
	},
	{
		ID:              "search_semantics_paths",
		Title:           "coder_glob/coder_grep path and filter semantics",
		Role:            "review",
		Capability:      "search_semantics",
		Tags:            []string{"live", "sandbox"},
		RequiresSandbox: true,
		Tools:           []string{"coder_glob", "coder_grep", "coder_list", "coder_read"},
		Prompt: "Real-world capability scenario SEARCH SEMANTICS. In the bound workspace: (1) list the workspace root " +
			"with coder_list and report the entry count you observe, (2) run coder_glob with the **/*.md idiom and " +
			"report whether top-level markdown files are matched, (3) run coder_grep for a literal that exists in a " +
			"top-level file and report the returned paths, (4) state whether the returned paths are relative to the " +
			"workspace root. Do not modify any file. Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the reported list count excludes the '.' self reference",
			"coder_glob with **/*.md matches files directly in the target directory",
			"grep result paths are workspace-root relative",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 371, 372, 404, 467},
	},
	{
		ID:              "confirmation_gate_reject",
		Title:           "Approve one mutation, reject the next, never retry it",
		Role:            "repl",
		Capability:      "confirmation_policy",
		Tags:            []string{"live", "sandbox", "mutating", "confirmation"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"coder_write", "coder_bash"},
		Prompt: "Real-world capability scenario CONFIRMATION GATE. Perform exactly two mutating steps and then stop. " +
			"Step 1: write cap_confirm.txt containing " + capabilityMarkerToken + ". Step 2: write a second file " +
			"cap_reject.txt. Each write requires my confirmation; I will approve the first and reject the second. " +
			"Do not retry a rejected action, do not attempt any other mutation and do not fabricate results. " +
			"Finish by stating exactly which action was approved and which was rejected, then output the marker " +
			capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"at least one confirmation was approved and at least one rejected",
			"the rejected action is not retried and no file is written for it",
			"the failed action reports a reason (never a bare 'Confirmed action failed')",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 525, 506, 548},
	},
	{
		ID:              "autonomous_run_no_confirm",
		Title:           "trust=allow run completes headless",
		Role:            "repl",
		Capability:      "autonomy",
		Tags:            []string{"live", "sandbox", "mutating", "autonomous"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"coder_write", "coder_edit", "coder_bash", "coder_git"},
		Prompt: "Real-world capability scenario AUTONOMOUS RUN. The chat is in autonomous (trust=allow) mode, so your " +
			"mutating tools run without a confirmation prompt. Create cap_autonomous.txt containing " +
			capabilityMarkerToken + ", commit it with coder_git on a new branch, and report the commit hash and branch " +
			"name. Do not ask for confirmation and do not stop before the commit. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the run never parks in needs_confirmation",
			"the branch and commit hash are reported and real (verifiable with coder_git)",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 506, 532, 548, 587},
	},
	{
		ID:              "auto_continue_incomplete_turn",
		Title:           "A turn that ends without a summary is continued to a PR",
		Role:            "repl",
		Capability:      "auto_continue",
		Tags:            []string{"live", "sandbox", "mutating", "autonomy"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"coder_bash", "coder_edit", "coder_git", "coder_todo_write"},
		Prompt: "Real-world capability scenario AUTO-CONTINUE. Implement a one-line change in the bound workspace " +
			"(add a comment line containing " + capabilityMarkerToken + " to a tracked file), commit it on a branch, " +
			"push it and open a draft pull request. Work through as many tool calls as you need; if a turn ends " +
			"without your summary, continue from the same workspace instead of restarting. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the interaction keeps moving after an incomplete turn (auto-continue fires)",
			"a branch and a draft PR exist at the end of the run",
			"the final turn carries a real summary (never an empty final text)",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 709, 724, 642},
	},
	{
		ID:              "typed_github_pr_flow",
		Title:           "Typed GitHub reads and verifiable writes",
		Role:            "review",
		Capability:      "typed_github",
		Tags:            []string{"live", "sandbox", "mutating", "github"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"github_issue_view", "github_pr_view", "github_pr_create", "github_pr_comment"},
		Prompt: "Real-world capability scenario TYPED GITHUB. Using only the typed GitHub tools (no coder_bash shell " +
			"escape): read the ticket tracker entry that is linked in this chat, list the open pull requests, then " +
			"post a comment on the linked issue containing the marker " + capabilityMarkerToken + " and report the " +
			"comment URL. Report the exact tool results. Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"no coder_bash call is needed for the GitHub work",
			"the comment write is re-read and reported as verified",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 514, 649},
	},
	{
		ID:              "plan_mode_readonly",
		Title:           "Plan mode mutates nothing but still publishes its plan",
		Role:            "plan",
		Capability:      "plan_mode",
		Tags:            []string{"live", "sandbox", "plan-mode"},
		RequiresSandbox: true,
		Tools:           []string{"coder_read", "coder_grep", "coder_list"},
		Prompt: "Real-world capability scenario PLAN MODE. Produce an implementation plan (3-5 steps) for adding a " +
			"one-line comment to a tracked file in the bound workspace. Plan mode must not mutate anything: do not " +
			"write, edit or commit. Report the plan and the tool calls you used to inspect the workspace. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"no mutating tool call is attempted or executed in plan mode",
			"the plan is readable in the chat transcript (or posted as an issue comment)",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 649},
	},
	{
		ID:              "context_budget_large_output",
		Title:           "A very large tool output does not overflow the context",
		Role:            "repl",
		Capability:      "context_budget",
		Tags:            []string{"live", "sandbox"},
		RequiresSandbox: true,
		Tools:           []string{"coder_bash", "coder_read", "coder_context"},
		Prompt: "Real-world capability scenario CONTEXT BUDGET. In the bound workspace produce a large amount of output " +
			"(eg `seq 1 200000` or a recursive listing of a large tree) with coder_bash, then answer: how many lines " +
			"did it produce, and was the output truncated or budgeted? Never let the generation fail with a context " +
			"limit error. Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the run never fails with 'exceeded maximum context'",
			"large tool results are truncated/budgeted before they are fed back to the model",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 680, 630},
	},
	{
		ID:              "tool_budget_cap_summary",
		Title:           "The per-chat tool budget caps the turn but keeps the summary",
		Role:            "repl",
		Capability:      "tool_budget",
		Tags:            []string{"live", "sandbox", "budget"},
		RequiresSandbox: true,
		Tools:           []string{"coder_bash", "coder_list"},
		Prompt: "Real-world capability scenario TOOL BUDGET. Do a broad exploration of the bound workspace: list and " +
			"read several directories, run a few shell probes. When you approach your tool budget, stop and write the " +
			"final summary instead of continuing to call tools. Report which tools you used and whether the budget was " +
			"reached. Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the turn ends with a non-empty final summary even at the tool budget",
			"the budget state is observable in the message metadata",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 709},
	},
	{
		ID:              "subagent_task_delegation",
		Title:           "Delegate an isolated subtask to coder_task",
		Role:            "lead",
		Capability:      "subagents",
		Tags:            []string{"live", "sandbox"},
		RequiresSandbox: true,
		Tools:           []string{"coder_task", "coder_read", "coder_grep"},
		Prompt: "Real-world capability scenario SUB-AGENTS. Delegate a small read-only investigation of the bound " +
			"workspace to a sub-agent with coder_task (eg 'report the top-level layout and the build entrypoint'), then " +
			"summarize the sub-agent's answer and state which tools it used. Do not modify any file. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the sub-agent ran with a restricted tool set in a fresh context",
			"the parent summarized the sub-agent result instead of re-running the whole investigation",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 514},
	},
	{
		ID:              "shell_modes_background_stream",
		Title:           "Background/streaming and interactive shell modes",
		Role:            "repl",
		Capability:      "shell_modes",
		Tags:            []string{"live", "sandbox", "mutating"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"coder_bash", "coder_read"},
		Prompt: "Real-world capability scenario SHELL MODES. In the bound workspace: (1) start a long-running command in " +
			"background mode (eg a loop that appends lines to cap_bg.log) and read part of its output, (2) send stdin to " +
			"an interactive command, (3) stop the background command. Report the log lines you observed and the exact " +
			"commands. Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"a background command was started, streamed from and terminated",
			"an interactive command received stdin",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 630},
	},
	{
		ID:         "hive_mind_dispatch_observe",
		Title:      "Dispatch and observe another agent through the hive mind",
		Role:       "lead",
		Capability: "hive_mind",
		Tags:       []string{"live", "hive-mind"},
		Tools:      []string{"hive_mind_agent_dispatch", "hive_mind_agent_list", "hive_mind_observe_list"},
		Prompt: "Real-world capability scenario HIVE MIND. Use the hive-mind tools to (1) list the agents you can " +
			"observe, (2) report the state of the most recent coding interaction for this repository, and (3) name the " +
			"capabilities of one peer bot. Do not dispatch any new agent for this scenario. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"at least one hive-mind read tool call succeeded",
			"the reported peer/agent information is taken from the tool result",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 540},
	},
	{
		ID:              "error_recovery_tool_failure",
		Title:           "A failing tool surfaces an error and the agent recovers",
		Role:            "repl",
		Capability:      "error_recovery",
		Tags:            []string{"live", "sandbox"},
		RequiresSandbox: true,
		Tools:           []string{"coder_bash", "coder_read"},
		Prompt: "Real-world capability scenario ERROR RECOVERY. Run a command that is guaranteed to fail (eg " +
			"`coder_bash` with `exit 3` and a non-existent path), report the verbatim error and exit code, then recover " +
			"by running an equivalent command that succeeds. Never claim success for the failed step. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the failing call reports a non-zero exit status and the verbatim error",
			"the recovery step succeeds and is reported separately",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 525, 703},
	},
	{
		ID:              "observability_tool_call_visibility",
		Title:           "Tool calls stay visible in the finished turn",
		Role:            "repl",
		Capability:      "observability",
		Tags:            []string{"live", "sandbox", "ui"},
		RequiresSandbox: true,
		Tools:           []string{"coder_list", "coder_read", "coder_bash"},
		Prompt: "Real-world capability scenario OBSERVABILITY. Run three read-only tool calls in the bound workspace " +
			"(list, read, and a shell probe) and then finish. Report, in the final message, the names of the tools you " +
			"called in order, so the transcript can be compared against the persisted tool-call metadata. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the finished message carries the tool calls in meta_data (not only the text)",
			"the persisted tool calls match the order reported in the reply",
			"no in-flight tool call is left in an 'ongoing' state after the turn finished",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 643, 655, 668, 723},
	},
	{
		ID:         "consolidation_merge_ready_prs",
		Title:      "Consolidate ready PRs and merge the green ones",
		Role:       "lead",
		Capability: "consolidation",
		Tags:       []string{"live", "github", "mutating"},
		Mutating:   true,
		Tools:      []string{"github_pr_view", "github_pr_merge", "github_pr_comment"},
		Prompt: "Real-world capability scenario CONSOLIDATION. List the open pull requests of this repository, report " +
			"for each one which required contexts are green, and state which ones are mergeable. Do not merge anything " +
			"for this scenario: report the exact check states you read. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"required vs informational contexts are distinguished correctly",
			"a mergeable PR is never merged when a required context is red",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 653, 683},
	},
	{
		ID:              "parallel_sessions_isolation",
		Title:           "Two concurrent sessions stay isolated",
		Role:            "repl",
		Capability:      "parallelism",
		Tags:            []string{"live", "sandbox", "parallel"},
		RequiresSandbox: true,
		Mutating:        true,
		Tools:           []string{"coder_select_workspace", "coder_bash", "coder_read"},
		Prompt: "Real-world capability scenario PARALLEL ISOLATION. Two chats run this scenario at the same time. " +
			"In this chat, write cap_isolation.txt containing the marker " + capabilityMarkerToken + " and report the " +
			"absolute path of the workspace you wrote it in. Then read back the file and report its content. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"the file is written and read back inside this chat's own workspace",
			"the workspace path differs from the concurrent chat's path",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 602, 662},
	},
	{
		ID:         "emergency_recovery_watchdog",
		Title:      "A restricted second-deployment bot monitors and recovers a crashed server",
		Role:       "admin",
		Capability: "emergency_recovery",
		Tags:       []string{"live", "ops", "ssh"},
		Tools:      []string{"ssh_exec_command_run_short", "service_restart", "chat_send_message"},
		Prompt: "Real-world capability scenario EMERGENCY RECOVERY (ci#658). You are the restricted recovery " +
			"agent of the ci2 deployment (see the \"Second CI deployment: ci2\" section of DEVELOPMENT.md). Using only the tools of that " +
			"profile: (1) run the read-only health and service-status probes against the watched host and report " +
			"their verbatim output, (2) if a probe failed, collect the recent logs/disk/memory and state the fault " +
			"you can actually see (if none, say so), (3) propose at most one remedy and wait for the confirmation " +
			"gate - never retry a rejected remedy, (4) post the incident summary to this chat. Do not report probe " +
			"output you did not observe, and never target production. " +
			"Finish with the exact marker " + capabilityMarkerToken + " on its own line.",
		Acceptance: []string{
			"only tools of the restricted recovery subset were used",
			"every reported probe result is verbatim tool output (no invented state)",
			"the remedy is confirmation-gated and a rejection is never retried",
			"the reply contains the marker nonce",
		},
		Tracks: []int{390, 658},
	},
}

// NativeCapabilityPlan returns the plan (classes + scenario registry) as a copy,
// so callers can marshal it without mutating the registry.
func NativeCapabilityPlan() CapabilityTestPlan {
	classes := make([]CapabilityClass, len(capabilityClasses))
	copy(classes, capabilityClasses)
	scenarios := make([]CapabilityScenario, len(capabilityScenarios))
	for i, scenario := range capabilityScenarios {
		scenario.Tags = append([]string(nil), scenario.Tags...)
		scenario.Tools = append([]string(nil), scenario.Tools...)
		scenario.Acceptance = append([]string(nil), scenario.Acceptance...)
		scenario.Tracks = append([]int(nil), scenario.Tracks...)
		scenarios[i] = scenario
	}
	return CapabilityTestPlan{Version: CapabilityPlanVersion, Classes: classes, Scenarios: scenarios}
}

// ScenarioByID returns the scenario with the given id.
func ScenarioByID(id string) (CapabilityScenario, bool) {
	for _, scenario := range capabilityScenarios {
		if scenario.ID == id {
			return scenario, true
		}
	}
	return CapabilityScenario{}, false
}

// ScenariosByTag returns every scenario carrying the tag, in registry order.
func ScenariosByTag(tag string) []CapabilityScenario {
	out := []CapabilityScenario{}
	for _, scenario := range capabilityScenarios {
		for _, candidate := range scenario.Tags {
			if candidate == tag {
				out = append(out, scenario)
				break
			}
		}
	}
	return out
}

// ScenariosByCapability returns every scenario covering a capability class.
func ScenariosByCapability(capability string) []CapabilityScenario {
	out := []CapabilityScenario{}
	for _, scenario := range capabilityScenarios {
		if scenario.Capability == capability {
			out = append(out, scenario)
		}
	}
	return out
}

// CapabilityCoverage maps every capability class to the scenario ids covering
// it, in registry order.
func CapabilityCoverage() map[string][]string {
	coverage := map[string][]string{}
	for _, scenario := range capabilityScenarios {
		coverage[scenario.Capability] = append(coverage[scenario.Capability], scenario.ID)
	}
	return coverage
}

// UncoveredCoreCapabilities returns the core capability classes that no scenario
// covers, sorted for a stable message.
func UncoveredCoreCapabilities() []string {
	coverage := CapabilityCoverage()
	uncovered := []string{}
	for _, class := range capabilityClasses {
		if class.Tier != CapabilityTierCore {
			continue
		}
		if len(coverage[class.Name]) == 0 {
			uncovered = append(uncovered, class.Name)
		}
	}
	sort.Strings(uncovered)
	return uncovered
}

// allowedToolPrefixes are the tool families a scenario may reference. A tool
// name outside them is a typo or a tool the coder does not have.
var allowedToolPrefixes = []string{"coder_", "github_", "hive_mind_", "mcp:", "chat_", "ssh_", "service_"}

// knownRoles are the native coder roles a scenario may be assigned to.
var knownRoles = []string{"lead", "repl", "review", "plan", "admin"}

// knownTiers are the valid capability tiers.
var knownTiers = []string{CapabilityTierCore, CapabilityTierExtended}

// ValidateCapabilityPlan checks a plan for the invariants the docs promise:
// stable unique ids, known roles/tiers, known tool families, non-empty
// acceptance criteria, marker-carrying prompts and full core coverage.
func ValidateCapabilityPlan(plan CapabilityTestPlan) []error {
	errs := []error{}
	if plan.Version <= 0 {
		errs = append(errs, fmt.Errorf("capability plan: version must be positive, got %d", plan.Version))
	}

	classes := map[string]CapabilityClass{}
	for _, class := range plan.Classes {
		if strings.TrimSpace(class.Name) == "" {
			errs = append(errs, fmt.Errorf("capability plan: class with empty name"))
			continue
		}
		if _, dup := classes[class.Name]; dup {
			errs = append(errs, fmt.Errorf("capability plan: duplicate class %q", class.Name))
		}
		if !containsString(knownTiers, class.Tier) {
			errs = append(errs, fmt.Errorf("capability plan: class %q has unknown tier %q", class.Name, class.Tier))
		}
		if strings.TrimSpace(class.Description) == "" {
			errs = append(errs, fmt.Errorf("capability plan: class %q has no description", class.Name))
		}
		classes[class.Name] = class
	}

	seen := map[string]bool{}
	covered := map[string]bool{}
	for _, scenario := range plan.Scenarios {
		label := scenario.ID
		if strings.TrimSpace(label) == "" {
			errs = append(errs, fmt.Errorf("capability plan: scenario with empty id"))
			label = "<empty id>"
		}
		if seen[label] {
			errs = append(errs, fmt.Errorf("capability plan: duplicate scenario id %q", label))
		}
		seen[label] = true

		if strings.TrimSpace(scenario.Title) == "" {
			errs = append(errs, fmt.Errorf("capability plan: scenario %q has no title", label))
		}
		if !containsString(knownRoles, scenario.Role) {
			errs = append(errs, fmt.Errorf("capability plan: scenario %q has unknown role %q", label, scenario.Role))
		}
		if _, ok := classes[scenario.Capability]; !ok {
			errs = append(errs, fmt.Errorf("capability plan: scenario %q covers unknown capability %q", label, scenario.Capability))
		} else {
			covered[scenario.Capability] = true
		}
		if len(scenario.Tags) == 0 {
			errs = append(errs, fmt.Errorf("capability plan: scenario %q has no tags", label))
		}
		if len(scenario.Tools) == 0 {
			errs = append(errs, fmt.Errorf("capability plan: scenario %q lists no tools", label))
		}
		for _, tool := range scenario.Tools {
			if !isKnownCoderTool(tool) {
				errs = append(errs, fmt.Errorf("capability plan: scenario %q references unknown tool %q", label, tool))
			}
		}
		if !strings.Contains(scenario.Prompt, capabilityMarkerToken) {
			errs = append(errs, fmt.Errorf("capability plan: scenario %q prompt does not carry the marker token", label))
		}
		if len(scenario.Acceptance) == 0 {
			errs = append(errs, fmt.Errorf("capability plan: scenario %q has no acceptance criteria", label))
		}
		for _, criterion := range scenario.Acceptance {
			if strings.TrimSpace(criterion) == "" {
				errs = append(errs, fmt.Errorf("capability plan: scenario %q has an empty acceptance criterion", label))
			}
		}
		if scenario.Mutating && !scenario.RequiresSandbox && !containsString(scenario.Tags, "github") {
			errs = append(errs, fmt.Errorf("capability plan: mutating scenario %q is neither sandboxed nor GitHub-scoped", label))
		}
	}

	for _, class := range plan.Classes {
		if class.Tier != CapabilityTierCore {
			continue
		}
		if !covered[class.Name] {
			errs = append(errs, fmt.Errorf("capability plan: core capability %q is not covered by any scenario", class.Name))
		}
	}
	return errs
}

// isKnownCoderTool reports whether the tool name belongs to a known coder tool
// family.
func isKnownCoderTool(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	for _, prefix := range allowedToolPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// containsString reports whether the slice contains the value.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// containsInt reports whether the slice contains the value.
func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
