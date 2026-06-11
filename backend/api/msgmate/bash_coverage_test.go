package msgmate

import (
	"strings"
	"testing"
)

// TestClassifyBashCommandBuckets covers the six ci#514 buckets plus the `other`
// fallback on the command shapes the ticket's evidence lists.
func TestClassifyBashCommandBuckets(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		category BashCategory
	}{
		{"gh issue list", "gh issue list --repo msgmate-io/open-chat-go-ci --state open --json number,title", BashCategoryGitHub},
		{"gh pr view", "gh pr view 873 --repo msgmate-io/open-chat-go-ci", BashCategoryGitHub},
		{"gh after cd wrapper", "cd /workspace/open-chat-go && gh pr diff 873", BashCategoryGitHub},
		{"gh with sudo", "sudo gh issue view 514", BashCategoryGitHub},
		{"ls", "ls -la backend", BashCategoryRepoInspection},
		{"needs its own segment", "ls | head -20", BashCategoryRepoInspection},
		{"grep", "rg -n 'coder_bash' backend --include=*.go", BashCategoryRepoInspection},
		{"git log", "git log --oneline -20", BashCategoryRepoInspection},
		{"go test", "go test ./backend/api/msgmate/...", BashCategoryBuildTestRun},
		{"gofmt", "gofmt -l backend", BashCategoryBuildTestRun},
		{"make", "make test", BashCategoryBuildTestRun},
		{"npm", "npm run lint --prefix frontend", BashCategoryBuildTestRun},
		{"mkdir", "mkdir -p backend/api/msgmate/tools", BashCategoryScaffold},
		{"curl", "curl -sSL https://example.test/x.tar.gz -o /tmp/x.tar.gz", BashCategoryScaffold},
		{"which", "which gh", BashCategoryEnvProbe},
		{"env", "env | sort", BashCategoryEnvProbe},
		{"uname", "uname -a", BashCategoryEnvProbe},
		{"openchat", "openchat-integrations list", BashCategoryIntegrationCLI},
		{"docker", "docker ps -a", BashCategoryIntegrationCLI},
		{"kubectl", "kubectl -n msgmate get pods", BashCategoryIntegrationCLI},
		{"bare path with dir", "/usr/local/bin/gh pr list", BashCategoryGitHub},
		{"python module run", "python -m pytest tests/", BashCategoryBuildTestRun},
		{"python script", "python3 scripts/gen.py --out /tmp/x", BashCategoryBuildTestRun},
		{"node script", "node scripts/check.js", BashCategoryBuildTestRun},
		{"interpreter one-liner", "node -e 'console.log(1)'", BashCategoryOther},
		{"python one-liner", "python3 -c 'import os;print(os.getcwd())'", BashCategoryOther},
		{"untouched other", "frobnicate --all", BashCategoryOther},
		{"empty", "", BashCategoryOther},
		{"whitespace", "   ", BashCategoryOther},
		{"unclassified binary", "frobnicate --all", BashCategoryOther},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ClassifyBashCommand(testCase.command); got != testCase.category {
				t.Fatalf("ClassifyBashCommand(%q) = %q, want %q", testCase.command, got, testCase.category)
			}
		})
	}
}

// TestClassifyBashCommandPrefersFirstClassifiedSegment pins the compound-command
// rule: a leading `cd` must not hide the real work, and a pure pipeline of
// inspection commands stays in the inspection bucket.
func TestClassifyBashCommandPrefersFirstClassifiedSegment(t *testing.T) {
	if got := ClassifyBashCommand("cd /x && go test ./... > /tmp/out.txt 2>&1"); got != BashCategoryBuildTestRun {
		t.Fatalf("compound build command classified as %q", got)
	}
	if got := ClassifyBashCommand("cat a.go; cat b.go"); got != BashCategoryRepoInspection {
		t.Fatalf("chained cat classified as %q", got)
	}
	if got := ClassifyBashCommand("VAR=1 go build ./..."); got != BashCategoryBuildTestRun {
		t.Fatalf("env-prefixed build classified as %q", got)
	}
	if got := ClassifyBashCommand("frobnicate && gh pr list"); got != BashCategoryGitHub {
		t.Fatalf("unclassified prefix hid gh: %q", got)
	}
}

// TestBuildBashCoverageReportAggregates counts buckets and the typed coverage
// fraction the ci#514 S1 metric publishes.
func TestBuildBashCoverageReportAggregates(t *testing.T) {
	report := BuildBashCoverageReport([]string{
		"gh pr view 873",
		"gh issue list --state open",
		"go test ./...",
		"ls backend",
		"frobnicate --all",
		"",
	})
	if report.Total != 5 {
		t.Fatalf("Total = %d, want 5", report.Total)
	}
	if report.ByCategory["github"] != 2 || report.ByCategory["build_test_run"] != 1 ||
		report.ByCategory["repo_inspection"] != 1 || report.ByCategory["other"] != 1 {
		t.Fatalf("unexpected buckets: %#v", report.ByCategory)
	}
	if report.TypedCoverable != 4 {
		t.Fatalf("TypedCoverable = %d, want 4", report.TypedCoverable)
	}
	if report.TypedCoveragePercent() != 80 {
		t.Fatalf("TypedCoveragePercent = %d, want 80", report.TypedCoveragePercent())
	}
	if len(report.UntypedExamples) != 1 || report.UntypedExamples[0] != "frobnicate --all" {
		t.Fatalf("untyped examples = %#v", report.UntypedExamples)
	}
	// Ordered most-frequent-first so the report reads like the ticket evidence.
	if categories := report.Categories(); categories[0] != "github" {
		t.Fatalf("Categories()[0] = %q, want github (%#v)", categories[0], categories)
	}
}

// TestBashCoverageReportEmpty covers the "nothing observed yet" case: no division
// by zero, no examples, a zero percent.
func TestBashCoverageReportEmpty(t *testing.T) {
	report := BuildBashCoverageReport(nil)
	if report.Total != 0 || report.TypedCoverable != 0 {
		t.Fatalf("empty report not empty: %#v", report)
	}
	if report.TypedCoveragePercent() != 0 || report.TypedCoverageFraction() != 0 {
		t.Fatalf("empty report has a non-zero coverage: %#v", report)
	}
	if len(report.Categories()) != 0 {
		t.Fatalf("empty report has categories: %#v", report.Categories())
	}
}

// TestBashSteeringHint covers the ci#514 S2 wording: a typed suggestion for
// covered categories and nothing to say for `other`.
func TestBashSteeringHint(t *testing.T) {
	hint := BashSteeringHint("gh pr view 873")
	if !strings.Contains(hint, "github") || !strings.Contains(hint, "github_pr_view") {
		t.Fatalf("github hint not typed: %q", hint)
	}
	if hint := BashSteeringHint("frobnicate --all"); hint != "" {
		t.Fatalf("other category produced a hint: %q", hint)
	}
}

// TestBashCommandsFromMessages extracts the commands of the coder_bash calls and
// ignores every other tool, so the per-turn coverage matches the real traffic.
func TestBashCommandsFromMessages(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "do the thing"},
		{
			"role": "assistant",
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id": "call-1",
					"function": map[string]interface{}{
						"name":      "coder_bash",
						"arguments": `{"command":"gh pr view 873"}`,
					},
				},
				map[string]interface{}{
					"id": "call-2",
					"function": map[string]interface{}{
						"name":      "coder_bash",
						"arguments": `{"commands":["gofmt -l backend","cd /x && go test ./api/msgmate/..."]}`,
					},
				},
				map[string]interface{}{
					"id": "call-3",
					"function": map[string]interface{}{
						"name":      "coder_git",
						"arguments": `{"path":"status"}`,
					},
				},
			},
		},
		{"role": "tool", "tool_call_id": "call-1", "content": "..."},
	}
	commands := BashCommandsFromMessages(messages)
	want := []string{"gh pr view 873", "gofmt -l backend", "cd /x && go test ./api/msgmate/..."}
	if len(commands) != len(want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	for index, command := range want {
		if commands[index] != command {
			t.Fatalf("command[%d] = %q, want %q", index, commands[index], command)
		}
	}
}

// TestBashCommandsFromMessagesTolerateGarbage keeps a malformed payload from
// breaking the request: it is dropped, never fatal.
func TestBashCommandsFromMessagesTolerateGarbage(t *testing.T) {
	messages := []map[string]interface{}{
		{
			"role": "assistant",
			"tool_calls": []interface{}{
				map[string]interface{}{
					"function": map[string]interface{}{"name": "coder_bash", "arguments": "not json"},
				},
				map[string]interface{}{
					"function": map[string]interface{}{"name": "coder_bash", "arguments": `{"command":"   "}`},
				},
				map[string]interface{}{
					"function": map[string]interface{}{"name": "coder_bash", "arguments": `{"commands":[{"command":"ls -la"}]}`},
				},
			},
		},
	}
	commands := BashCommandsFromMessages(messages)
	if len(commands) != 1 || commands[0] != "ls -la" {
		t.Fatalf("commands = %#v, want [ls -la]", commands)
	}
}

// TestBashCoverageMetadataShape pins the keys the coder_context payload and the CI
// coverage publication consume.
func TestBashCoverageMetadataShape(t *testing.T) {
	metadata := BuildBashCoverageReport([]string{"gh pr list", "frobnicate"}).Metadata()
	for _, key := range []string{"total", "by_category", "typed_coverable", "typed_coverage_percent", "untyped_examples"} {
		if _, ok := metadata[key]; !ok {
			t.Fatalf("metadata misses %q: %#v", key, metadata)
		}
	}
	if metadata["typed_coverage_percent"] != 50 {
		t.Fatalf("typed_coverage_percent = %v, want 50", metadata["typed_coverage_percent"])
	}
}

// TestBuildContextUsageReportIncludesBashCoverage wires the classifier into the
// measured report (ci#514 S1), so a chat that used coder_bash publishes coverage.
func TestBuildContextUsageReportIncludesBashCoverage(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "please open the PR"},
		{
			"role": "assistant",
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id": "call-1",
					"function": map[string]interface{}{
						"name":      "coder_bash",
						"arguments": `{"command":"gh pr create --base main --title x"}`,
					},
				},
			},
		},
		{"role": "tool", "tool_call_id": "call-1", "content": "https://example.test/pr/1"},
	}
	report := BuildContextUsageReport(messages, 10000, ContextCompactionStats{}, ResolveToolResultLimits(nil), 0)
	if report.BashCoverage.Total != 1 {
		t.Fatalf("BashCoverage.Total = %d, want 1", report.BashCoverage.Total)
	}
	if report.BashCoverage.ByCategory["github"] != 1 {
		t.Fatalf("BashCoverage buckets = %#v", report.BashCoverage.ByCategory)
	}
	if _, ok := report.Metadata()["bash_coverage"]; !ok {
		t.Fatalf("Metadata misses bash_coverage: %#v", report.Metadata())
	}
}

// TestBuildContextUsageReportNoBashOmitsCoverage keeps the payload unchanged for
// chats that never touched the shell.
func TestBuildContextUsageReportNoBashOmitsCoverage(t *testing.T) {
	messages := []map[string]interface{}{{"role": "user", "content": "hi"}}
	report := BuildContextUsageReport(messages, 10000, ContextCompactionStats{}, ResolveToolResultLimits(nil), 0)
	if report.BashCoverage.Total != 0 {
		t.Fatalf("unexpected coverage: %#v", report.BashCoverage)
	}
	if _, ok := report.Metadata()["bash_coverage"]; ok {
		t.Fatalf("bash_coverage present without bash calls: %#v", report.Metadata())
	}
}
