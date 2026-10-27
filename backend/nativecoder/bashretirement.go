package nativecoder

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// This file is the machine-readable form of the coder_bash retirement plan
// (ci#514): the umbrella plan that replaces the shell escape hatch with typed,
// policy-scoped integration tools (GitHub first).
//
// It contains three things:
//
//  1. The **plan itself** as data: the observed shell usage categories, the
//     phase that retires each one with its exit criteria, and the typed tool
//     surface that replaces it (`BashRetirementPlanSnapshot`).
//  2. A deterministic **classifier** (`ClassifyShellCommand`,
//     `ClassifyBashCallSample`) that buckets one historical `coder_bash` /
//     `coder_git` call into exactly one category. It is the core of the
//     "Phase 0 - Measure" offline job; the job itself lives in
//     `backend/scripts/bash_retirement_report`.
//  3. The **coverage metric** (`ComputeBashCoverage`): the share of historical
//     shell calls that the typed surface can express, per category and overall.
//     A phase is done when its categories drop below
//     `BashRetirementShareThreshold` and the documented exit criteria hold.
//
// The classifier is deliberately a conservative heuristic: a command whose
// binary or git subcommand is not recognised is *not* claimed as covered, it
// lands in the guarded `residual_shell` bucket. Over-claiming coverage would
// hide exactly the capability loss the plan is supposed to prevent.
//
// No secrets, tokens or deployment configuration are part of this plan: it
// contains category metadata, tool names and command *shapes* only.

// BashRetirementPlanVersion is the plan schema version. It is reported with
// every coverage run so evidence can be attributed to a plan revision.
const BashRetirementPlanVersion = 1

const (
	// BashRetirementDeprecatedTool is the tool the plan retires: the universal
	// shell escape hatch.
	BashRetirementDeprecatedTool = "coder_bash"
	// BashRetirementResidualTool is the narrow, allow-listed replacement that
	// survives the migration for genuinely arbitrary work.
	BashRetirementResidualTool = "coder_shell"
	// BashRetirementShareThreshold is the share of observed shell calls below
	// which a category counts as retired. A category that is expressible by
	// typed tools *and* used less than this fraction of the time is done.
	BashRetirementShareThreshold = 0.02
)

// The usage categories. `BashCategoryResidualShell` is the only category that
// stays on the shell after the migration; every other category must end up
// expressed by typed tools.
const (
	BashCategoryGitHubCLI      = "github_cli"
	BashCategoryRepoInspection = "repo_inspection"
	BashCategoryGitMutation    = "git_mutation"
	BashCategoryBuildTestRun   = "build_test_run"
	BashCategoryScaffolding    = "scaffolding_fixtures"
	BashCategoryEnvProbing     = "env_service_probing"
	BashCategoryIntegrationCLI = "integration_clis"
	BashCategoryResidualShell  = "residual_shell"
)

// BashUsageCategory is one recurring shape of historical `coder_bash` usage,
// the typed tool surface that replaces it and the policy that guards it.
type BashUsageCategory struct {
	// ID is the stable category identifier used by the classifier, the metric
	// and the docs.
	ID string `json:"id"`
	// Title is a one-line human description.
	Title string `json:"title"`
	// Description explains what the category covers and why it is typed out.
	Description string `json:"description"`
	// Tier is CapabilityTierCore (must be retired) or CapabilityTierExtended.
	Tier string `json:"tier"`
	// Phase is the retirement phase ID that owns this category.
	Phase string `json:"phase"`
	// Shipped reports whether the typed surface replacing this category exists
	// today (as opposed to being planned by its phase).
	Shipped bool `json:"shipped"`
	// Guarded marks the single residual escape hatch category.
	Guarded bool `json:"guarded,omitempty"`
	// Examples are real historical command shapes (no secrets) the classifier
	// must map into this category.
	Examples []string `json:"examples"`
	// TargetTools are the typed tools (existing or to be shipped) that express
	// this category without a shell.
	TargetTools []string `json:"target_tools"`
	// Policy is the permission scope the typed surface needs.
	Policy string `json:"policy,omitempty"`
	// Notes carry migration caveats for the category.
	Notes []string `json:"notes,omitempty"`
}

// BashRetirementPhase is one phase of the plan: the categories it retires, its
// goal and the criteria that must hold before the next phase starts.
type BashRetirementPhase struct {
	// ID is the stable phase identifier.
	ID string `json:"id"`
	// Order is the phase sequence, starting at 0 (measure) with no gaps.
	Order int `json:"order"`
	// Title is a one-line human description.
	Title string `json:"title"`
	// Goal states what the phase delivers.
	Goal string `json:"goal"`
	// Categories are the usage categories this phase retires. Phase 0 (measure)
	// retires none, it defines the metric.
	Categories []string `json:"categories"`
	// ExitCriteria are the observable conditions that close the phase.
	ExitCriteria []string `json:"exit_criteria"`
	// Tracks are the ci ticket numbers the phase is the evidence for.
	Tracks []int `json:"tracks"`
}

// BashRetirementPlan is the whole plan: its schema version, the usage
// categories and the ordered phases.
type BashRetirementPlan struct {
	Version           int                   `json:"version"`
	DeprecatedTool    string                `json:"deprecated_tool"`
	ResidualTool      string                `json:"residual_tool"`
	CoverageThreshold float64               `json:"coverage_threshold"`
	Categories        []BashUsageCategory   `json:"categories"`
	Phases            []BashRetirementPhase `json:"phases"`
}

// bashUsageCategories is the category registry of the plan.
var bashUsageCategories = []BashUsageCategory{
	{
		ID:          BashCategoryGitHubCLI,
		Title:       "GitHub CLI workflows",
		Description: "Dev-agent bread and butter: reading the tracker, reading a PR and its checks, creating a PR, commenting, review requests. Semantically typed already, so it must never need a shell.",
		Tier:        CapabilityTierCore,
		Phase:       "phase_1_github",
		Shipped:     true,
		Examples: []string{
			"gh issue list --repo msgmate-io/open-chat-go-ci --state open --json number,title",
			"gh issue view 514 --repo msgmate-io/open-chat-go-ci --json body",
			"gh pr view 873 --json files,statusCheckRollup",
			"gh pr diff 873",
			"gh pr create --title X --body Y --base main",
			"gh pr comment 873 --body '...'",
			"gh api repos/msgmate-io/open-chat-go-ci/pulls/873/reviews",
			"gh run list --repo msgmate-io/open-chat-go --limit 10 --status in_progress",
			"gh run view 123456789 --repo msgmate-io/open-chat-go --json status,jobs",
			"gh auth status",
		},
		TargetTools: []string{
			"github_issue_view", "github_issue_list", "github_issue_create", "github_issue_comment",
			"github_pr_view", "github_pr_list", "github_pr_create", "github_pr_comment", "github_pr_review",
			"github_pr_merge", "github_auth_status", "github_api",
			"github_run_list", "github_run_view",
		},
		Policy: "read/write per tool; github_pr_merge is admin scope",
		Notes: []string{
			"the write tools return the created object, so a posted review/comment is verifiable instead of claimed",
			"github_api stays read-only and allow-listed for the long tail",
		},
	},
	{
		ID:          BashCategoryRepoInspection,
		Title:       "Repo inspection",
		Description: "Reading the workspace and its git history: listing, reading, grepping and the read-only git plumbing. Mostly covered by the native tools already; the residual is git history/plumbing.",
		Tier:        CapabilityTierCore,
		Phase:       "phase_3_git_plumbing",
		Shipped:     true,
		Examples: []string{
			"pwd; ls -la",
			"git status",
			"git log --oneline -20",
			"git diff --stat",
			"git show HEAD",
			"git remote -v",
			"git cat-file -p HEAD",
			"wc -l backend/nativecoder/*.go",
			"grep -rn coder_bash backend | head -20",
			"cat DEVELOPMENT.md",
		},
		TargetTools: []string{
			"coder_list", "coder_read", "coder_glob", "coder_grep",
			"coder_git_log", "coder_git_show", "coder_git_diff", "coder_git_blame", "coder_git_remote",
		},
		Policy: "read-only",
		Notes: []string{
			"coder_read/list/glob/grep cover the filesystem part today",
			"git history/plumbing (log, show, blame, remote, cat-file) must move onto coder_git reads",
		},
	},
	{
		ID:          BashCategoryGitMutation,
		Title:       "Git mutation",
		Description: "Branching, committing, pushing and the repo plumbing that changes refs. Already the typed core of coder_git; the plan keeps shell out of it entirely.",
		Tier:        CapabilityTierCore,
		Phase:       "phase_3_git_plumbing",
		Shipped:     true,
		Examples: []string{
			"git checkout -b fix/514-typed-tools",
			"git add backend/nativecoder",
			"git commit -m 'feat: typed surface'",
			"git push -u origin fix/514-typed-tools",
			"git fetch origin main",
			"git stash push -m wip",
			"git tag v0.0.752",
		},
		TargetTools: []string{"coder_git_branch", "coder_git_checkout", "coder_git_add", "coder_git_commit", "coder_git_push", "coder_git_fetch", "coder_git_stash", "coder_git_tag"},
		Policy:      "write; push is scoped to the chat's own branch and never to the default branch",
		Notes: []string{
			"the typed surface enforces the repo's own playbook: never push to main, never push an empty commit",
		},
	},
	{
		ID:          BashCategoryBuildTestRun,
		Title:       "Build / test / lint / run",
		Description: "The toolchain: go/npm/cargo/python builds, tests and the produced programs. Wrapped as raw shell today, so exit codes and diagnostics have to be scraped from stdout.",
		Tier:        CapabilityTierCore,
		Phase:       "phase_2_build_test_run",
		Shipped:     false,
		Examples: []string{
			"go build ./...",
			"go test ./backend/nativecoder/...",
			"gofmt -l backend/nativecoder",
			"npm ci",
			"npm run lint -- --quiet",
			"cargo test --quiet",
			"python3 cap_scenario/main.py",
		},
		TargetTools: []string{"coder_build", "coder_test", "coder_lint", "coder_run"},
		Policy:      "write (artifacts/test output), no network unless the project needs a dependency install",
		Notes: []string{
			"the typed tools detect the project (go/npm/cargo/python) and return structured diagnostics: exit status, file:line, failing test names",
			"they must call the same toolchain under the hood, so they are no slower than the shell equivalent",
		},
	},
	{
		ID:          BashCategoryScaffolding,
		Title:       "Project scaffolding / fixtures",
		Description: "Creating directories, writing scratch files and fixtures, and running a scenario script that produces them. Replaced by the typed file writers plus the project-aware runner.",
		Tier:        CapabilityTierCore,
		Phase:       "phase_2_build_test_run",
		Shipped:     true,
		Examples: []string{
			"mkdir -p cap_scenario",
			"touch cap_isolation.txt",
			"echo marker > cap_isolation.txt",
			"cp -r testdata/fixtures /tmp/fixtures",
			"rm -rf ./build-scratch",
			"mktemp -d",
		},
		TargetTools: []string{"coder_write", "coder_edit", "coder_list", "coder_run"},
		Policy:      "write, scoped to the chat's own workspace",
		Notes: []string{
			"coder_write/coder_edit cover file creation and editing today",
			"a scenario script that scaffolds files is a coder_run call, not a shell call",
		},
	},
	{
		ID:          BashCategoryEnvProbing,
		Title:       "Environment and service probing",
		Description: "Looking at the environment and at running services: env vars, binaries on PATH, processes, logs, dev servers, outbound HTTP probes.",
		Tier:        CapabilityTierCore,
		Phase:       "phase_4_integration_clis",
		Shipped:     false,
		Examples: []string{
			"env",
			"command -v go",
			"ps aux",
			"tail -n 50 dev.log",
			"curl -s http://localhost:8080/healthz",
			"npm run dev &",
		},
		TargetTools: []string{"coder_env", "coder_process", "coder_logs", "coder_webfetch", "coder_serve"},
		Policy:      "read-only except coder_serve (background process, chat-scoped)",
		Notes: []string{
			"coder_webfetch already covers outbound HTTP probes",
			"a long-running dev server becomes a typed background process instead of `cmd &` plus a log scrape",
		},
	},
	{
		ID:          BashCategoryIntegrationCLI,
		Title:       "Integration CLIs",
		Description: "kubectl, docker, ssh, helm and friends: every one of these is a domain the platform already owns through an integration, so the typed tool must be owned by that integration and exposed through the Host boundary.",
		Tier:        CapabilityTierCore,
		Phase:       "phase_4_integration_clis",
		Shipped:     false,
		Examples: []string{
			"kubectl get pods -n open-chat",
			"kubectl describe deployment open-chat",
			"kubectl logs deploy/open-chat --tail 100",
			"docker ps --format '{{.Names}}'",
			"docker compose up -d",
			"ssh coder@10.0.0.5 'ls /workspace'",
			"helm upgrade --install open-chat ./chart",
		},
		TargetTools: []string{
			"kubernetes_get", "kubernetes_describe", "kubernetes_logs",
			DockerToolCheck, DockerToolPS, DockerToolLogs, DockerToolBuild, DockerToolRun, DockerToolCompose, DockerToolExec,
			"ssh_run",
		},
		Policy: "read for get/describe/logs/ps; write for exec/up/upgrade, per integration",
		Notes: []string{
			"each tool is owned by its integration (ci#423 Host boundary), never absorbed by the core",
		},
	},
	{
		ID:          BashCategoryResidualShell,
		Title:       "Guarded residual escape hatch",
		Description: "Genuinely arbitrary work: custom build systems, interactive REPLs, package-installer oddities and anything the classifier does not recognise. This is the only category that stays on a shell, and only through a narrow allow-listed tool.",
		Tier:        CapabilityTierExtended,
		Phase:       "phase_5_guarded_residual",
		Guarded:     true,
		Examples: []string{
			"./custom-build.sh --target wasm",
			"my-unrecognised-tool --flags",
			"python3 -i",
		},
		TargetTools: []string{BashRetirementResidualTool},
		Policy:      "explicit per-chat opt-in, allow-listed binaries/arguments, plan-mode preview",
		Notes: []string{
			"the classifier never claims an unrecognised binary or git subcommand as covered, it lands here",
			"the escape hatch stays reachable so the migration cannot lose capability",
		},
	},
}

// bashRetirementPhases is the ordered phase plan.
var bashRetirementPhases = []BashRetirementPhase{
	{
		ID:    "phase_0_measure",
		Order: 0,
		Title: "Measure",
		Goal:  "Bucket every historical coder_bash/coder_git call by category and repo and define the coverage metric, so every later phase can be judged by evidence instead of by feel.",
		ExitCriteria: []string{
			"every historical shell call can be bucketed by the classifier (unclassified calls are a tracked residual)",
			"the coverage metric (expressible share, per category and overall) is computed and recorded",
			"a baseline per category is stored so a phase can be shown to have moved it",
		},
		Tracks: []int{514},
	},
	{
		ID:         "phase_1_github",
		Order:      1,
		Title:      "Typed GitHub tools (highest value)",
		Goal:       "Ship the typed GitHub surface (issues, PRs, review, repo/search, auth status, read-only api) alongside coder_bash and switch the dev-agent prompts to typed-first.",
		Categories: []string{BashCategoryGitHubCLI},
		ExitCriteria: []string{
			"the typed GitHub tools are shipped and declared by the coding integration",
			"the coder system prompt prefers the typed tools for every GitHub workflow",
			"the github_cli share of shell calls is below the retirement threshold",
			"a write (comment/review/PR) is re-read and reported as verified instead of claimed",
			"the capability plan's typed-GitHub scenario completes without a coder_bash call",
		},
		Tracks: []int{514, 649, 390},
	},
	{
		ID:         "phase_2_build_test_run",
		Order:      2,
		Title:      "Build / test / lint / run (project-aware)",
		Goal:       "Replace raw toolchain shell with coder_build/coder_test/coder_lint/coder_run that detect the project and return structured diagnostics plus artifacts.",
		Categories: []string{BashCategoryBuildTestRun, BashCategoryScaffolding},
		ExitCriteria: []string{
			"the four tools detect go/npm/cargo/python projects",
			"they return exit status, file:line diagnostics and failing test names as structure, not as scraped text",
			"they are no slower than the shell equivalent (same toolchain under the hood)",
			"the build_test_run and scaffolding_fixtures shares are below the retirement threshold",
		},
		Tracks: []int{514, 390},
	},
	{
		ID:         "phase_3_git_plumbing",
		Order:      3,
		Title:      "Git history and plumbing",
		Goal:       "Move the read-only git inspection that still shells out (log, show, blame, remote, cat-file) and the mutation plumbing onto coder_git reads/writes.",
		Categories: []string{BashCategoryRepoInspection, BashCategoryGitMutation},
		ExitCriteria: []string{
			"coder_git covers log/show/diff/blame/remote reads and branch/add/commit/push writes",
			"no dev-agent prompt instructs a raw git command",
			"the repo_inspection and git_mutation shares are below the retirement threshold",
			"the typed git surface still refuses to push the default branch or an empty commit",
		},
		Tracks: []int{514, 703},
	},
	{
		ID:         "phase_4_integration_clis",
		Order:      4,
		Title:      "Integration CLIs and service probing",
		Goal:       "Give kubernetes/docker_sandbox/ssh and the service-probing needs their own typed tools, owned by the relevant integration and exposed through the stable Host interface, so agents never shell out to those CLIs.",
		Categories: []string{BashCategoryIntegrationCLI, BashCategoryEnvProbing},
		ExitCriteria: []string{
			"the recurring kubectl/docker/ssh operations have typed tools with structured results",
			"each tool is registered by its own integration, not by the core",
			"dev servers and log tailing run as typed background processes",
			"the integration_clis and env_service_probing shares are below the retirement threshold",
		},
		Tracks: []int{514, 423},
	},
	{
		ID:         "phase_5_guarded_residual",
		Order:      5,
		Title:      "Guarded residual",
		Goal:       "Make coder_bash default-off and replace it with the narrow, allow-listed coder_shell (plan-mode preview, explicit per-chat opt-in) for genuinely arbitrary work.",
		Categories: []string{BashCategoryResidualShell},
		ExitCriteria: []string{
			"coder_bash is retired for the dev-agent roles and coder_shell is opt-in per chat",
			"coder_shell enforces an allow-list of binaries/arguments and previews in plan mode",
			"the residual share is measured and documented, and never silently grows",
			"no capability was lost: the residual scenarios still complete through coder_shell",
		},
		Tracks: []int{514},
	},
}

// BashRetirementPlanSnapshot returns a deep copy of the plan, so a caller can
// neither mutate the registry nor observe a mutation of it.
func BashRetirementPlanSnapshot() BashRetirementPlan {
	plan := BashRetirementPlan{
		Version:           BashRetirementPlanVersion,
		DeprecatedTool:    BashRetirementDeprecatedTool,
		ResidualTool:      BashRetirementResidualTool,
		CoverageThreshold: BashRetirementShareThreshold,
		Categories:        make([]BashUsageCategory, len(bashUsageCategories)),
		Phases:            make([]BashRetirementPhase, len(bashRetirementPhases)),
	}
	for i, category := range bashUsageCategories {
		plan.Categories[i] = category
		plan.Categories[i].Examples = append([]string(nil), category.Examples...)
		plan.Categories[i].TargetTools = append([]string(nil), category.TargetTools...)
		plan.Categories[i].Notes = append([]string(nil), category.Notes...)
	}
	for i, phase := range bashRetirementPhases {
		plan.Phases[i] = phase
		plan.Phases[i].Categories = append([]string(nil), phase.Categories...)
		plan.Phases[i].ExitCriteria = append([]string(nil), phase.ExitCriteria...)
		plan.Phases[i].Tracks = append([]int(nil), phase.Tracks...)
	}
	return plan
}

// BashRetirementCategoryByID resolves one category of the plan.
func BashRetirementCategoryByID(id string) (BashUsageCategory, bool) {
	id = strings.TrimSpace(id)
	for _, category := range bashUsageCategories {
		if category.ID == id {
			return category, true
		}
	}
	return BashUsageCategory{}, false
}

// BashRetirementTargetTools returns the sorted, de-duplicated typed tool names
// the plan replaces the shell with. It is the allow-list a coding integration
// declares to run the migration scenario without a shell.
func BashRetirementTargetTools() []string {
	seen := map[string]struct{}{}
	for _, category := range bashUsageCategories {
		for _, tool := range category.TargetTools {
			tool = strings.TrimSpace(tool)
			if tool == "" || tool == BashRetirementDeprecatedTool || tool == BashRetirementResidualTool {
				continue
			}
			seen[tool] = struct{}{}
		}
	}
	tools := make([]string, 0, len(seen))
	for tool := range seen {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

// BashRetirementPlanJSON renders the plan as deterministic, indented JSON: the
// offline classifier job and the live harness both consume this payload.
func BashRetirementPlanJSON() ([]byte, error) {
	payload, err := json.MarshalIndent(BashRetirementPlanSnapshot(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

// ValidateBashRetirementPlan checks the registry's internal consistency: every
// category is owned by exactly one phase, every phase is ordered without gaps,
// the residual escape hatch is the only guarded category, and no category
// claims the retired tool as its typed replacement.
func ValidateBashRetirementPlan() error {
	return validateBashRetirementPlan(BashRetirementPlanSnapshot())
}

func validateBashRetirementPlan(plan BashRetirementPlan) error {
	if plan.Version != BashRetirementPlanVersion {
		return fmt.Errorf("bash retirement plan version %d is not the supported %d", plan.Version, BashRetirementPlanVersion)
	}
	if plan.DeprecatedTool != BashRetirementDeprecatedTool {
		return fmt.Errorf("bash retirement plan deprecates %q, want %q", plan.DeprecatedTool, BashRetirementDeprecatedTool)
	}
	if plan.ResidualTool != BashRetirementResidualTool {
		return fmt.Errorf("bash retirement plan residual tool is %q, want %q", plan.ResidualTool, BashRetirementResidualTool)
	}
	if plan.CoverageThreshold <= 0 || plan.CoverageThreshold >= 1 {
		return fmt.Errorf("bash retirement coverage threshold %v is not a share in (0,1)", plan.CoverageThreshold)
	}
	if len(plan.Categories) == 0 {
		return fmt.Errorf("bash retirement plan declares no usage category")
	}
	if len(plan.Phases) == 0 {
		return fmt.Errorf("bash retirement plan declares no phase")
	}

	phases := map[string]BashRetirementPhase{}
	for _, phase := range plan.Phases {
		if strings.TrimSpace(phase.ID) == "" {
			return fmt.Errorf("bash retirement plan has a phase without an id")
		}
		if _, exists := phases[phase.ID]; exists {
			return fmt.Errorf("bash retirement plan has a duplicate phase %q", phase.ID)
		}
		if strings.TrimSpace(phase.Title) == "" || strings.TrimSpace(phase.Goal) == "" {
			return fmt.Errorf("bash retirement phase %q needs a title and a goal", phase.ID)
		}
		if len(phase.ExitCriteria) == 0 {
			return fmt.Errorf("bash retirement phase %q needs at least one exit criterion", phase.ID)
		}
		phases[phase.ID] = phase
	}
	orders := make([]int, 0, len(plan.Phases))
	for _, phase := range plan.Phases {
		orders = append(orders, phase.Order)
	}
	sort.Ints(orders)
	for i, order := range orders {
		if order != i {
			return fmt.Errorf("bash retirement phase order %v is not the contiguous sequence starting at 0", orders)
		}
	}

	seen := map[string]int{}
	houses := map[string]int{}
	for _, category := range plan.Categories {
		if strings.TrimSpace(category.ID) == "" {
			return fmt.Errorf("bash retirement plan has a category without an id")
		}
		if _, exists := seen[category.ID]; exists {
			return fmt.Errorf("bash retirement plan has a duplicate category %q", category.ID)
		}
		seen[category.ID] = 1
		if strings.TrimSpace(category.Title) == "" || strings.TrimSpace(category.Description) == "" {
			return fmt.Errorf("bash retirement category %q needs a title and a description", category.ID)
		}
		if category.Tier != CapabilityTierCore && category.Tier != CapabilityTierExtended {
			return fmt.Errorf("bash retirement category %q has unknown tier %q", category.ID, category.Tier)
		}
		if len(category.Examples) == 0 {
			return fmt.Errorf("bash retirement category %q needs at least one observed command shape", category.ID)
		}
		if len(category.TargetTools) == 0 {
			return fmt.Errorf("bash retirement category %q needs a typed target tool surface", category.ID)
		}
		if _, exists := phases[category.Phase]; !exists {
			return fmt.Errorf("bash retirement category %q references unknown phase %q", category.ID, category.Phase)
		}
		houses[category.Phase]++
		if category.Guarded {
			if category.ID != BashCategoryResidualShell {
				return fmt.Errorf("bash retirement category %q is guarded but is not the residual escape hatch", category.ID)
			}
			if category.Tier != CapabilityTierExtended {
				return fmt.Errorf("the residual escape hatch %q must be an extended tier capability", category.ID)
			}
			if len(category.TargetTools) != 1 || category.TargetTools[0] != BashRetirementResidualTool {
				return fmt.Errorf("the residual escape hatch must target only %q, got %v", BashRetirementResidualTool, category.TargetTools)
			}
			continue
		}
		for _, tool := range category.TargetTools {
			if tool == BashRetirementDeprecatedTool || tool == BashRetirementResidualTool {
				return fmt.Errorf("bash retirement category %q claims %q as its typed replacement", category.ID, tool)
			}
		}
	}
	if _, exists := seen[BashCategoryResidualShell]; !exists {
		return fmt.Errorf("bash retirement plan must declare the residual escape hatch category %q", BashCategoryResidualShell)
	}

	for _, phase := range plan.Phases {
		for _, id := range phase.Categories {
			if _, exists := seen[id]; !exists {
				return fmt.Errorf("bash retirement phase %q references unknown category %q", phase.ID, id)
			}
			owner := ""
			for _, category := range plan.Categories {
				if category.ID == id {
					owner = category.Phase
				}
			}
			if owner != phase.ID {
				return fmt.Errorf("bash retirement category %q is owned by %q but listed in %q", id, owner, phase.ID)
			}
		}
	}
	if total := len(houses); total > len(plan.Phases) {
		return fmt.Errorf("bash retirement plan houses categories in %d phases but declares %d", total, len(plan.Phases))
	}
	return nil
}

// BashCallSample is one observed shell-tool call of a historical transcript:
// the tool name plus its raw arguments, and optionally the repository it ran
// against (the offline job groups the metric by repo as well).
type BashCallSample struct {
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Repo      string `json:"repo,omitempty"`
}

// BashCallClassification is the classifier's verdict for one call: the commands
// it carries, the category of every segment and whether the whole call is
// expressible by the typed surface.
type BashCallClassification struct {
	Tool       string   `json:"tool"`
	Commands   []string `json:"commands"`
	Categories []string `json:"categories"`
	// Category is the call's primary bucket: the first segment's category.
	Category string `json:"category"`
	// Covered is true only when every segment of every command maps to a typed
	// category (ie no segment lands in the residual escape hatch).
	Covered bool `json:"covered"`
}

// BashCoverageBucket is one row of the coverage metric.
type BashCoverageBucket struct {
	Category    string   `json:"category"`
	Calls       int      `json:"calls"`
	Share       float64  `json:"share"`
	Expressible bool     `json:"expressible"`
	Shipped     bool     `json:"shipped"`
	Retired     bool     `json:"retired"`
	TargetTools []string `json:"target_tools"`
}

// BashCoverageReport is the ci#514 coverage metric over a set of historical
// shell calls: how much of the observed shell usage the typed surface can
// express today, per category and overall.
type BashCoverageReport struct {
	Version               int                  `json:"version"`
	DeprecatedTool        string               `json:"deprecated_tool"`
	Threshold             float64              `json:"threshold"`
	TotalCalls            int                  `json:"total_calls"`
	ClassifiedCalls       int                  `json:"classified_calls"`
	ErroredCalls          int                  `json:"errored_calls"`
	CoveredCalls          int                  `json:"covered_calls"`
	ResidualCalls         int                  `json:"residual_calls"`
	CoverageRatio         float64              `json:"coverage_ratio"`
	ShippedCalls          int                  `json:"shipped_calls"`
	ShippedRatio          float64              `json:"shipped_ratio"`
	Buckets               []BashCoverageBucket `json:"buckets"`
	RetiredCategories     []string             `json:"retired_categories"`
	OutstandingCategories []string             `json:"outstanding_categories"`
}

// bashBinaryCategories maps a shell binary onto its usage category. It is a
// conservative heuristic for Phase 0 measurement: an unlisted binary is not
// claimed as covered, it lands in the residual escape hatch.
var bashBinaryCategories = map[string]string{
	"gh": BashCategoryGitHubCLI,

	"pwd": BashCategoryRepoInspection, "ls": BashCategoryRepoInspection, "cat": BashCategoryRepoInspection,
	"head": BashCategoryRepoInspection, "wc": BashCategoryRepoInspection, "grep": BashCategoryRepoInspection,
	"rg": BashCategoryRepoInspection, "find": BashCategoryRepoInspection, "file": BashCategoryRepoInspection,
	"stat": BashCategoryRepoInspection, "tree": BashCategoryRepoInspection, "jq": BashCategoryRepoInspection,
	"sort": BashCategoryRepoInspection, "uniq": BashCategoryRepoInspection, "cut": BashCategoryRepoInspection,
	"sed": BashCategoryRepoInspection, "awk": BashCategoryRepoInspection, "cd": BashCategoryRepoInspection,
	"echo": BashCategoryRepoInspection, "printf": BashCategoryRepoInspection,

	"mkdir": BashCategoryScaffolding, "touch": BashCategoryScaffolding, "rm": BashCategoryScaffolding,
	"cp": BashCategoryScaffolding, "mv": BashCategoryScaffolding, "ln": BashCategoryScaffolding,
	"chmod": BashCategoryScaffolding, "tee": BashCategoryScaffolding, "mktemp": BashCategoryScaffolding,
	"tar": BashCategoryScaffolding, "unzip": BashCategoryScaffolding, "truncate": BashCategoryScaffolding,

	"go": BashCategoryBuildTestRun, "gofmt": BashCategoryBuildTestRun, "golangci-lint": BashCategoryBuildTestRun,
	"npm": BashCategoryBuildTestRun, "npx": BashCategoryBuildTestRun, "pnpm": BashCategoryBuildTestRun,
	"yarn": BashCategoryBuildTestRun, "node": BashCategoryBuildTestRun, "tsc": BashCategoryBuildTestRun,
	"jest": BashCategoryBuildTestRun, "vitest": BashCategoryBuildTestRun, "vite": BashCategoryBuildTestRun,
	"cargo": BashCategoryBuildTestRun, "rustc": BashCategoryBuildTestRun, "make": BashCategoryBuildTestRun,
	"cmake": BashCategoryBuildTestRun, "gradle": BashCategoryBuildTestRun, "mvn": BashCategoryBuildTestRun,
	"python": BashCategoryBuildTestRun, "python3": BashCategoryBuildTestRun, "pip": BashCategoryBuildTestRun,
	"pip3": BashCategoryBuildTestRun, "pytest": BashCategoryBuildTestRun, "poetry": BashCategoryBuildTestRun,

	"env": BashCategoryEnvProbing, "printenv": BashCategoryEnvProbing, "which": BashCategoryEnvProbing,
	"type": BashCategoryEnvProbing, "ps": BashCategoryEnvProbing, "pgrep": BashCategoryEnvProbing,
	"top": BashCategoryEnvProbing, "systemctl": BashCategoryEnvProbing, "journalctl": BashCategoryEnvProbing,
	"ss": BashCategoryEnvProbing, "netstat": BashCategoryEnvProbing, "lsof": BashCategoryEnvProbing,
	"curl": BashCategoryEnvProbing, "wget": BashCategoryEnvProbing, "nc": BashCategoryEnvProbing,
	"ping": BashCategoryEnvProbing, "dig": BashCategoryEnvProbing, "tail": BashCategoryEnvProbing,

	"kubectl": BashCategoryIntegrationCLI, "helm": BashCategoryIntegrationCLI, "oc": BashCategoryIntegrationCLI,
	"docker": BashCategoryIntegrationCLI, "docker-compose": BashCategoryIntegrationCLI, "podman": BashCategoryIntegrationCLI,
	"minikube": BashCategoryIntegrationCLI, "kind": BashCategoryIntegrationCLI, "k9s": BashCategoryIntegrationCLI,
	"ssh": BashCategoryIntegrationCLI, "scp": BashCategoryIntegrationCLI, "rsync": BashCategoryIntegrationCLI,
	"terraform": BashCategoryIntegrationCLI, "ansible-playbook": BashCategoryIntegrationCLI,
	"psql": BashCategoryIntegrationCLI, "mysql": BashCategoryIntegrationCLI, "redis-cli": BashCategoryIntegrationCLI,
}

// bashWrapperBinaries are prefixes that carry no category of their own; the
// classifier strips them and classifies the command they wrap.
var bashWrapperBinaries = map[string]bool{
	"sudo": true, "time": true, "nohup": true, "exec": true, "command": true, "builtin": true, "xargs": true,
}

// bashNavigationBinaries only move the shell's working directory. They are
// context for the segments that follow them, not the work of the call, so a
// `cd backend && gh pr view` is a GitHub call, not an inspection.
var bashNavigationBinaries = map[string]bool{"cd": true, "pushd": true, "popd": true}

// bashRedirectWriterBinaries are the inspection binaries whose only interesting
// behaviour is producing a file. With an output redirection they are
// scaffolding, not inspection.
var bashRedirectWriterBinaries = map[string]bool{
	"echo": true, "printf": true, "cat": true, "seq": true, "base64": true,
}

// bashGitReadOnlySubcommands are the git subcommands that only read: they are
// repo inspection, not a mutation.
var bashGitReadOnlySubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "remote": true, "cat-file": true,
	"rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true, "show-ref": true,
	"describe": true, "blame": true, "shortlog": true, "config": true, "grep": true, "version": true,
	"check-ignore": true, "merge-base": true, "diff-tree": true, "whatchanged": true, "help": true,
}

// bashIsGitToolName reports whether a tool name belongs to the typed git tool
// family, whose arguments carry a git subcommand rather than a shell command.
func bashIsGitToolName(tool string) bool {
	tool = strings.ToLower(strings.TrimSpace(tool))
	return tool == "coder_git" || strings.HasPrefix(tool, "coder_git_")
}

// ClassifyGitSubcommand maps a git subcommand onto its usage category. An
// unknown or missing subcommand is treated as a mutation: the classifier errs
// towards "still needs the typed git surface" rather than towards "covered".
func ClassifyGitSubcommand(subcommand string) string {
	sub := strings.ToLower(strings.TrimSpace(subcommand))
	sub = strings.TrimLeft(sub, "-")
	if sub == "" {
		return BashCategoryGitMutation
	}
	if bashGitReadOnlySubcommands[sub] {
		return BashCategoryRepoInspection
	}
	return BashCategoryGitMutation
}

// ClassifyShellCommand returns the usage category of one shell command, based
// on its first segment. A command shape the classifier does not recognise is
// residual_shell. An empty command is residual as well.
func ClassifyShellCommand(command string) string {
	segments := ClassifyShellSegments(command)
	if len(segments) == 0 {
		return BashCategoryResidualShell
	}
	return segments[0]
}

// ClassifyShellSegments returns the usage category of every segment of a shell
// command, in order. A segment is split on `;`, `&&`, `||`, `|` and newlines,
// which is a heuristic: quoting is not interpreted, so an unusual command can
// over-segment. That is safe for the metric - a mis-split segment only ever
// pushes a call towards the residual bucket, never away from it.
func ClassifyShellSegments(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n")
	segments := make([]string, 0, 4)
	for _, raw := range strings.Split(replacer.Replace(command), "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		segment := classifyShellSegment(raw)
		if segment == "" {
			// Pure navigation (`cd backend && …`): the following segment is
			// the work.
			continue
		}
		segments = append(segments, segment)
	}
	return segments
}

// classifyShellSegment classifies a single (already split) shell segment. An
// empty result means the segment carries no category of its own (pure
// navigation) and is skipped by the caller.
func classifyShellSegment(segment string) string {
	words := strings.Fields(segment)
	// `command -v x` / `type -p x` are PATH lookups: environment probing.
	if len(words) >= 2 && (words[0] == "command" || words[0] == "type") && strings.HasPrefix(words[1], "-") {
		return BashCategoryEnvProbing
	}
	for len(words) > 0 {
		word := words[0]
		if bashIsEnvAssignment(word) || bashWrapperBinaries[word] {
			words = words[1:]
			continue
		}
		if strings.HasPrefix(word, "-") {
			words = words[1:]
			continue
		}
		break
	}
	if len(words) == 0 {
		return BashCategoryResidualShell
	}
	binary := path.Base(words[0])
	if bashNavigationBinaries[binary] {
		// `cd backend && <work>` is the work that follows, not the cd itself.
		return ""
	}
	if bashRedirectWriterBinaries[binary] && strings.Contains(segment, ">") {
		// `echo marker > file` produces a file: scaffolding, not inspection.
		return BashCategoryScaffolding
	}
	if binary == "git" {
		return ClassifyGitSubcommand(bashGitSubcommand(words[1:]))
	}
	if category, known := bashBinaryCategories[binary]; known {
		return category
	}
	return BashCategoryResidualShell
}

// bashIsEnvAssignment reports whether a shell word is a leading `NAME=value`
// assignment rather than a binary.
func bashIsEnvAssignment(word string) bool {
	index := strings.Index(word, "=")
	if index <= 0 {
		return false
	}
	name := word[:index]
	for i, r := range name {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit && r != '_' {
			return false
		}
		if i == 0 && isDigit {
			return false
		}
	}
	return true
}

// bashGitSubcommand picks the subcommand out of the words that follow `git`,
// skipping global flags (and the `-C <dir>` style options' values).
func bashGitSubcommand(words []string) string {
	skipNext := false
	for _, word := range words {
		if skipNext {
			skipNext = false
			continue
		}
		if word == "-C" || word == "-c" || word == "--git-dir" || word == "--work-tree" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(word, "-") {
			// `--version` carries the intent itself.
			if len(word) > 2 && !strings.Contains(word, "=") {
				return strings.TrimLeft(word, "-")
			}
			continue
		}
		return word
	}
	return ""
}

// ExtractShellCommands pulls the shell commands out of one tool call's raw
// arguments. A bare JSON string is a single command. A typed git tool is
// normalised to `git <subcommand>` so it is classified by the same rules.
func ExtractShellCommands(tool, arguments string) ([]string, error) {
	tool = strings.TrimSpace(tool)
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return nil, fmt.Errorf("%s: empty arguments", tool)
	}

	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &fields); err != nil {
		var single string
		if errSingle := json.Unmarshal([]byte(arguments), &single); errSingle != nil {
			return nil, fmt.Errorf("%s: invalid arguments: %w", tool, err)
		}
		single = strings.TrimSpace(single)
		if single == "" {
			return nil, fmt.Errorf("%s: empty command", tool)
		}
		return []string{single}, nil
	}

	if bashIsGitToolName(tool) {
		for _, key := range []string{"subcommand", "action", "command"} {
			if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
				return []string{"git " + strings.TrimSpace(value)}, nil
			}
		}
		return nil, fmt.Errorf("%s: no subcommand in arguments", tool)
	}

	commands := make([]string, 0, 2)
	var appendValue func(value interface{})
	appendValue = func(value interface{}) {
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				commands = append(commands, trimmed)
			}
		case []interface{}:
			for _, item := range typed {
				appendValue(item)
			}
		}
	}
	for _, key := range []string{"command", "cmd", "script", "commands"} {
		if value, exists := fields[key]; exists {
			appendValue(value)
		}
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("%s: no command in arguments", tool)
	}
	return commands, nil
}

// ClassifyBashCallSample classifies one historical shell-tool call.
func ClassifyBashCallSample(sample BashCallSample) (BashCallClassification, error) {
	commands, err := ExtractShellCommands(sample.Tool, sample.Arguments)
	if err != nil {
		return BashCallClassification{}, err
	}
	classification := BashCallClassification{
		Tool:     strings.TrimSpace(sample.Tool),
		Commands: commands,
		Covered:  true,
	}
	for _, command := range commands {
		categories := ClassifyShellSegments(command)
		if len(categories) == 0 {
			categories = []string{BashCategoryResidualShell}
		}
		for _, category := range categories {
			classification.Categories = append(classification.Categories, category)
			if category == BashCategoryResidualShell {
				classification.Covered = false
			}
		}
	}
	if len(classification.Categories) > 0 {
		classification.Category = classification.Categories[0]
	} else {
		classification.Category = BashCategoryResidualShell
		classification.Covered = false
	}
	return classification, nil
}

// ComputeBashCoverage computes the ci#514 coverage metric over historical shell
// calls: how many of them the typed surface can express, per category and
// overall. Calls whose arguments cannot be parsed are reported separately as
// errored calls; they are never counted as covered.
func ComputeBashCoverage(samples []BashCallSample) BashCoverageReport {
	plan := BashRetirementPlanSnapshot()
	report := BashCoverageReport{
		Version:        plan.Version,
		DeprecatedTool: plan.DeprecatedTool,
		Threshold:      plan.CoverageThreshold,
		TotalCalls:     len(samples),
	}
	counts := map[string]int{}
	for _, sample := range samples {
		classification, err := ClassifyBashCallSample(sample)
		if err != nil {
			report.ErroredCalls++
			continue
		}
		report.ClassifiedCalls++
		counts[classification.Category]++
		category, known := BashRetirementCategoryByID(classification.Category)
		if classification.Covered && known && !category.Guarded {
			report.CoveredCalls++
			if category.Shipped {
				report.ShippedCalls++
			}
			continue
		}
		report.ResidualCalls++
	}
	if report.ClassifiedCalls > 0 {
		denominator := float64(report.ClassifiedCalls)
		report.CoverageRatio = float64(report.CoveredCalls) / denominator
		report.ShippedRatio = float64(report.ShippedCalls) / denominator
	}

	for _, category := range plan.Categories {
		bucket := BashCoverageBucket{
			Category:    category.ID,
			Calls:       counts[category.ID],
			Expressible: !category.Guarded && len(category.TargetTools) > 0,
			Shipped:     category.Shipped,
			TargetTools: append([]string(nil), category.TargetTools...),
		}
		if report.ClassifiedCalls > 0 {
			bucket.Share = float64(bucket.Calls) / float64(report.ClassifiedCalls)
		}
		bucket.Retired = bucket.Expressible && bucket.Share < plan.CoverageThreshold
		if bucket.Retired {
			report.RetiredCategories = append(report.RetiredCategories, category.ID)
		} else if bucket.Expressible {
			report.OutstandingCategories = append(report.OutstandingCategories, category.ID)
		}
		report.Buckets = append(report.Buckets, bucket)
	}
	sort.SliceStable(report.Buckets, func(i, j int) bool {
		if report.Buckets[i].Calls != report.Buckets[j].Calls {
			return report.Buckets[i].Calls > report.Buckets[j].Calls
		}
		return report.Buckets[i].Category < report.Buckets[j].Category
	})
	sort.Strings(report.RetiredCategories)
	sort.Strings(report.OutstandingCategories)
	return report
}
