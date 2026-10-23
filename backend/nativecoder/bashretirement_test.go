package nativecoder

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateBashRetirementPlan(t *testing.T) {
	if err := ValidateBashRetirementPlan(); err != nil {
		t.Fatalf("the shipped plan must be internally consistent: %v", err)
	}
}

func TestBashRetirementPlanSnapshotIsACopy(t *testing.T) {
	first := BashRetirementPlanSnapshot()
	first.Categories[0].TargetTools[0] = "mutated"
	first.Phases[0].ExitCriteria[0] = "mutated"
	if second := BashRetirementPlanSnapshot(); second.Categories[0].TargetTools[0] == "mutated" || second.Phases[0].ExitCriteria[0] == "mutated" {
		t.Fatal("BashRetirementPlanSnapshot must not expose the registry")
	}
}

func TestBashRetirementTargetToolsExcludeRetiredTools(t *testing.T) {
	tools := BashRetirementTargetTools()
	if len(tools) == 0 {
		t.Fatal("the plan must name a typed target surface")
	}
	for _, tool := range tools {
		if tool == BashRetirementDeprecatedTool || tool == BashRetirementResidualTool {
			t.Fatalf("the typed surface must not list %q", tool)
		}
	}
	for i := 1; i < len(tools); i++ {
		if tools[i-1] >= tools[i] {
			t.Fatalf("target tools must be sorted and unique, got %v", tools)
		}
	}
}

func TestBashRetirementPlanJSONIsDeterministic(t *testing.T) {
	first, err := BashRetirementPlanJSON()
	if err != nil {
		t.Fatalf("render plan: %v", err)
	}
	second, err := BashRetirementPlanJSON()
	if err != nil {
		t.Fatalf("render plan: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("the plan JSON must be deterministic")
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Fatal("the plan JSON must end with a newline")
	}
	var decoded BashRetirementPlan
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("the plan JSON must decode: %v", err)
	}
	if decoded.Version != BashRetirementPlanVersion || len(decoded.Phases) != len(bashRetirementPhases) {
		t.Fatalf("unexpected plan payload %+v", decoded)
	}
}

func TestClassifyShellCommand(t *testing.T) {
	cases := map[string]string{
		"gh issue list --repo msgmate-io/open-chat-go-ci --state open --json number": BashCategoryGitHubCLI,
		"gh pr create --title x --body y --base main":                                BashCategoryGitHubCLI,
		"cd backend && gh pr view 873 --json files":                                  BashCategoryGitHubCLI,
		"pwd; ls -la":                              BashCategoryRepoInspection,
		"git log --oneline -20":                    BashCategoryRepoInspection,
		"git -C /workspace status":                 BashCategoryRepoInspection,
		"git checkout -b fix/514":                  BashCategoryGitMutation,
		"git commit -m 'feat: typed'":              BashCategoryGitMutation,
		"go test ./backend/nativecoder/...":        BashCategoryBuildTestRun,
		"npm run lint -- --quiet":                  BashCategoryBuildTestRun,
		"mkdir -p cap_scenario":                    BashCategoryScaffolding,
		"echo marker > cap_isolation.txt":          BashCategoryScaffolding,
		"command -v go":                            BashCategoryEnvProbing,
		"FOO=bar grep -rn x backend | head -5":     BashCategoryRepoInspection,
		"kubectl logs deploy/open-chat --tail 100": BashCategoryIntegrationCLI,
		"docker ps --format '{{.Names}}'":          BashCategoryIntegrationCLI,
		"my-unrecognised-tool --flags":             BashCategoryResidualShell,
		"":                                         BashCategoryResidualShell,
	}
	for command, want := range cases {
		if got := ClassifyShellCommand(command); got != want {
			t.Errorf("ClassifyShellCommand(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestClassifyGitSubcommand(t *testing.T) {
	for sub, want := range map[string]string{
		"status": BashCategoryRepoInspection, "cat-file": BashCategoryRepoInspection,
		"blame": BashCategoryRepoInspection, "push": BashCategoryGitMutation,
		"fetch": BashCategoryGitMutation, "": BashCategoryGitMutation, "definitely-not-git": BashCategoryGitMutation,
	} {
		if got := ClassifyGitSubcommand(sub); got != want {
			t.Errorf("ClassifyGitSubcommand(%q) = %q, want %q", sub, got, want)
		}
	}
}

func TestExtractShellCommands(t *testing.T) {
	if commands, err := ExtractShellCommands("coder_bash", `{"command":"ls -la"}`); err != nil || len(commands) != 1 || commands[0] != "ls -la" {
		t.Fatalf("object arguments: %v %v", commands, err)
	}
	if commands, err := ExtractShellCommands("coder_bash", `"go build ./..."`); err != nil || len(commands) != 1 {
		t.Fatalf("bare string arguments: %v %v", commands, err)
	}
	commands, err := ExtractShellCommands("coder_git", `{"subcommand":"log","args":["-5"]}`)
	if err != nil || len(commands) != 1 || commands[0] != "git log" {
		t.Fatalf("typed git tool: %v %v", commands, err)
	}
	for _, arguments := range []string{"", "{}", "not-json"} {
		if _, err := ExtractShellCommands("coder_bash", arguments); err == nil {
			t.Fatalf("arguments %q should not parse into a command", arguments)
		}
	}
}

func TestComputeBashCoverage(t *testing.T) {
	samples := []BashCallSample{
		{Tool: "coder_bash", Arguments: `{"command":"gh pr view 873 --json files"}`, Repo: "msgmate-io/open-chat-go"},
		{Tool: "coder_bash", Arguments: `{"command":"gh issue list --repo x/y"}`},
		{Tool: "coder_bash", Arguments: `{"command":"go test ./backend/nativecoder/..."}`},
		{Tool: "coder_git", Arguments: `{"subcommand":"checkout","args":["-b","fix/514"]}`},
		{Tool: "coder_bash", Arguments: `{"command":"my-unrecognised-tool --flags"}`},
		{Tool: "coder_bash", Arguments: "not-json"},
	}
	report := ComputeBashCoverage(samples)
	if report.TotalCalls != len(samples) || report.ClassifiedCalls != len(samples)-1 || report.ErroredCalls != 1 {
		t.Fatalf("unexpected call accounting: %+v", report)
	}
	if report.CoveredCalls != 4 || report.ResidualCalls != 1 {
		t.Fatalf("unexpected coverage: %+v", report)
	}
	if report.CoverageRatio <= 0.79 || report.CoverageRatio >= 0.81 {
		t.Fatalf("coverage ratio %v should be ~0.8", report.CoverageRatio)
	}
	if report.ShippedCalls != 3 {
		t.Fatalf("the shipped gh/git calls must be counted as shipped, got %d", report.ShippedCalls)
	}
	byID := map[string]BashCoverageBucket{}
	for _, bucket := range report.Buckets {
		byID[bucket.Category] = bucket
	}
	if byID[BashCategoryGitHubCLI].Calls != 2 || byID[BashCategoryBuildTestRun].Calls != 1 {
		t.Fatalf("unexpected buckets %+v", byID)
	}
	if byID[BashCategoryResidualShell].Calls != 1 || byID[BashCategoryResidualShell].Expressible {
		t.Fatalf("the residual call must be bucketed and not expressible by typed tools: %+v", byID[BashCategoryResidualShell])
	}
	if byID[BashCategoryGitHubCLI].Retired {
		t.Fatalf("a category above the retirement threshold must stay outstanding: %+v", byID[BashCategoryGitHubCLI])
	}
	found := false
	for _, id := range report.OutstandingCategories {
		if id == BashCategoryGitHubCLI {
			found = true
		}
	}
	if !found {
		t.Fatalf("github_cli must be outstanding until the prompts go typed-first: %v", report.OutstandingCategories)
	}
}

func TestComputeBashCoverageEmpty(t *testing.T) {
	report := ComputeBashCoverage(nil)
	if report.TotalCalls != 0 || report.ClassifiedCalls != 0 || report.CoverageRatio != 0 {
		t.Fatalf("an empty sample must report a zero metric: %+v", report)
	}
	if len(report.Buckets) != len(bashUsageCategories) {
		t.Fatalf("every category must be reported, got %d buckets", len(report.Buckets))
	}
}
