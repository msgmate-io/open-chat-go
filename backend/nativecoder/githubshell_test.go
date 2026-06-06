package nativecoder

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateGitHubShellCommandIssueVerbs(t *testing.T) {
	cases := []struct {
		name      string
		command   string
		tool      string
		mutating  bool
		assertion func(t *testing.T, call GitHubToolCall)
	}{
		{
			name:    "issue list with repo state json limit",
			command: "gh issue list --repo msgmate-io/open-chat-go-ci --state open --json number,title --limit 20",
			tool:    GitHubToolIssueList,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["repo"] != "msgmate-io/open-chat-go-ci" {
					t.Fatalf("repo = %v", call.Arguments["repo"])
				}
				if call.Arguments["state"] != "open" {
					t.Fatalf("state = %v", call.Arguments["state"])
				}
				if call.Arguments["limit"] != 20 {
					t.Fatalf("limit = %v (%T)", call.Arguments["limit"], call.Arguments["limit"])
				}
				fields, ok := call.Arguments["fields"].([]string)
				if !ok || len(fields) != 2 || fields[0] != "number" || fields[1] != "title" {
					t.Fatalf("fields = %v", call.Arguments["fields"])
				}
			},
		},
		{
			name:    "issue view by number",
			command: "gh issue view 514 --repo msgmate-io/open-chat-go-ci --json body,title",
			tool:    GitHubToolIssueView,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["number"] != 514 {
					t.Fatalf("number = %v", call.Arguments["number"])
				}
			},
		},
		{
			name:     "issue create is a write",
			command:  "gh issue create --repo msgmate-io/open-chat-go-ci --title 'a title' --body 'a body'",
			tool:     GitHubToolIssueCreate,
			mutating: true,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["title"] != "a title" || call.Arguments["body"] != "a body" {
					t.Fatalf("args = %v", call.Arguments)
				}
			},
		},
		{
			name:     "issue comment is a write",
			command:  "gh issue comment 514 --body 'noted'",
			tool:     GitHubToolIssueComment,
			mutating: true,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["number"] != 514 || call.Arguments["body"] != "noted" {
					t.Fatalf("args = %v", call.Arguments)
				}
			},
		},
		{
			name:    "leading environment assignment is stripped",
			command: "GH_TOKEN=redacted gh issue view 514 --json title",
			tool:    GitHubToolIssueView,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if _, leak := call.Arguments["GH_TOKEN"]; leak {
					t.Fatal("the environment assignment must never reach the typed call")
				}
				if call.Arguments["number"] != 514 {
					t.Fatalf("number = %v", call.Arguments["number"])
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			translation := TranslateGitHubShellCommand(testCase.command)
			if !translation.Translated {
				t.Fatalf("expected translation, got residual: %s", translation.Reason)
			}
			if translation.Call == nil {
				t.Fatal("translated command must carry a typed call")
			}
			if translation.Call.Tool != testCase.tool {
				t.Fatalf("tool = %s, want %s", translation.Call.Tool, testCase.tool)
			}
			if translation.Call.Mutating != testCase.mutating {
				t.Fatalf("mutating = %v, want %v", translation.Call.Mutating, testCase.mutating)
			}
			if strings.TrimSpace(translation.Call.Reason) == "" {
				t.Fatal("a typed call must explain itself to the model")
			}
			testCase.assertion(t, *translation.Call)
		})
	}
}

func TestTranslateGitHubShellCommandPullRequestVerbs(t *testing.T) {
	cases := []struct {
		name      string
		command   string
		tool      string
		mutating  bool
		admin     bool
		assertion func(t *testing.T, call GitHubToolCall)
	}{
		{
			name:    "pr list",
			command: "gh pr list --repo msgmate-io/open-chat-go-ci --state open --json number,title,headRefName",
			tool:    GitHubToolPRList,
			assertion: func(t *testing.T, call GitHubToolCall) {
				fields, ok := call.Arguments["fields"].([]string)
				if !ok || len(fields) != 3 {
					t.Fatalf("fields = %v", call.Arguments["fields"])
				}
			},
		},
		{
			name:    "pr view with check rollup fields",
			command: "gh pr view 873 --json files,statusCheckRollup",
			tool:    GitHubToolPRView,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["number"] != 873 {
					t.Fatalf("number = %v", call.Arguments["number"])
				}
				if _, includeDiff := call.Arguments["include_diff"]; includeDiff {
					t.Fatal("a plain view must not ask for the diff")
				}
			},
		},
		{
			name:    "pr diff folds into the view",
			command: "gh pr diff 873",
			tool:    GitHubToolPRView,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["include_diff"] != true {
					t.Fatalf("args = %v", call.Arguments)
				}
			},
		},
		{
			name:    "pr checks folds into the view",
			command: "gh pr checks 873 --repo msgmate-io/open-chat-go-ci",
			tool:    GitHubToolPRView,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["include_checks"] != true {
					t.Fatalf("args = %v", call.Arguments)
				}
			},
		},
		{
			name:     "draft pr create",
			command:  "gh pr create --title 't' --body 'b' --base main --head feature/x --draft",
			tool:     GitHubToolPRCreate,
			mutating: true,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["base"] != "main" || call.Arguments["head"] != "feature/x" {
					t.Fatalf("args = %v", call.Arguments)
				}
				if call.Arguments["draft"] != true {
					t.Fatalf("draft = %v", call.Arguments["draft"])
				}
			},
		},
		{
			name:     "pr comment",
			command:  "gh pr comment 873 --body 'pushed the fix'",
			tool:     GitHubToolPRComment,
			mutating: true,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["body"] != "pushed the fix" {
					t.Fatalf("body = %v", call.Arguments["body"])
				}
			},
		},
		{
			name:     "pr review approve",
			command:  "gh pr review 873 --approve --body 'lgtm'",
			tool:     GitHubToolPRReview,
			mutating: true,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["event"] != "APPROVE" {
					t.Fatalf("event = %v", call.Arguments["event"])
				}
			},
		},
		{
			name:     "pr review request changes",
			command:  "gh pr review 873 --request-changes --body 'needs tests'",
			tool:     GitHubToolPRReview,
			mutating: true,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["event"] != "REQUEST_CHANGES" {
					t.Fatalf("event = %v", call.Arguments["event"])
				}
			},
		},
		{
			name:     "pr merge squash through the admin-scoped tool",
			command:  "gh pr merge 873 --squash --delete-branch",
			tool:     GitHubToolPRMerge,
			mutating: true,
			admin:    true,
			assertion: func(t *testing.T, call GitHubToolCall) {
				if call.Arguments["method"] != "squash" {
					t.Fatalf("method = %v", call.Arguments["method"])
				}
				if call.Arguments["delete_branch"] != true {
					t.Fatalf("delete_branch = %v", call.Arguments["delete_branch"])
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			translation := TranslateGitHubShellCommand(testCase.command)
			if !translation.Translated || translation.Call == nil {
				t.Fatalf("expected translation, got residual: %s", translation.Reason)
			}
			if translation.Call.Tool != testCase.tool {
				t.Fatalf("tool = %s, want %s", translation.Call.Tool, testCase.tool)
			}
			if translation.Call.Mutating != testCase.mutating {
				t.Fatalf("mutating = %v, want %v", translation.Call.Mutating, testCase.mutating)
			}
			if translation.Call.Admin != testCase.admin {
				t.Fatalf("admin = %v, want %v", translation.Call.Admin, testCase.admin)
			}
			testCase.assertion(t, *translation.Call)
		})
	}
}

func TestTranslateGitHubShellCommandAuthAndAPI(t *testing.T) {
	translation := TranslateGitHubShellCommand("gh auth status")
	if !translation.Translated || translation.Call.Tool != GitHubToolAuthStatus {
		t.Fatalf("gh auth status must translate to %s, got %+v", GitHubToolAuthStatus, translation)
	}

	translation = TranslateGitHubShellCommand("gh api repos/msgmate-io/open-chat-go-ci/pulls/873/reviews")
	if !translation.Translated || translation.Call.Tool != GitHubToolAPI {
		t.Fatalf("read-only gh api must translate to %s, got %+v", GitHubToolAPI, translation)
	}
	if translation.Call.Arguments["method"] != "GET" {
		t.Fatalf("method = %v", translation.Call.Arguments["method"])
	}
	if translation.Call.Arguments["path"] != "/repos/msgmate-io/open-chat-go-ci/pulls/873/reviews" {
		t.Fatalf("path = %v", translation.Call.Arguments["path"])
	}
	if translation.Call.Mutating {
		t.Fatal("a read-only API call must not be marked mutating")
	}

	translation = TranslateGitHubShellCommand("gh api --paginate /repos/x/y/issues")
	if !translation.Translated || translation.Call.Arguments["paginate"] != true {
		t.Fatalf("--paginate must carry through: %+v", translation)
	}
}

func TestTranslateGitHubShellCommandRefusesCredentialAndWriteEscapes(t *testing.T) {
	residuals := []string{
		"gh auth token",
		"gh auth login",
		"gh pr merge 873 --admin --unknown-flag", // still a merge, must not be dropped
		"gh api -X POST /repos/x/y/issues",
		"gh api /repos/x/y/issues -f title=t",
		"gh api graphql -f query='{ viewer { login } }'",
		"gh repo clone msgmate-io/open-chat-go",
		"gh gist create file.txt",
		"gh extension install x/y",
		"gh issue view --json title", // no number
		"gh pr review 873 --body 'no verdict'",
		"gh",
		"",
	}
	for _, command := range residuals {
		translation := TranslateGitHubShellCommand(command)
		switch command {
		case "gh pr merge 873 --admin --unknown-flag":
			if !translation.Translated || translation.Call.Tool != GitHubToolPRMerge {
				t.Fatalf("%q must still translate: %+v", command, translation)
			}
			continue
		}
		if translation.Translated {
			t.Fatalf("%q must stay on the guarded residual, got %+v", command, translation.Call)
		}
		if !translation.Residual {
			t.Fatalf("%q must be flagged residual", command)
		}
		if strings.TrimSpace(translation.Reason) == "" {
			t.Fatalf("%q must carry a residual reason", command)
		}
	}

	token := TranslateGitHubShellCommand("gh auth token")
	if !strings.Contains(strings.ToLower(token.Reason), "credential") {
		t.Fatalf("credential refusal must say so: %s", token.Reason)
	}
}

func TestTranslateGitHubShellCommandIgnoresNonGitHubPipelines(t *testing.T) {
	translation := TranslateGitHubShellCommand("git status && gh issue list")
	if translation.Translated {
		t.Fatal("a non-gh leading command is not this bridge's job")
	}
	if !translation.Residual {
		t.Fatal("a non-gh command must be residual")
	}

	// A gh command followed by a pipe is still translated as the gh part; the
	// remainder is classified separately by the segment classifier.
	translation = TranslateGitHubShellCommand("gh issue list --json number | head -5")
	if !translation.Translated || translation.Call.Tool != GitHubToolIssueList {
		t.Fatalf("piped gh command must translate: %+v", translation)
	}
}

func TestGitHubShellTranslationTargetsMatchThePlan(t *testing.T) {
	category, ok := BashRetirementCategoryByID(BashCategoryGitHubCLI)
	if !ok {
		t.Fatalf("the plan must declare the %s category", BashCategoryGitHubCLI)
	}
	planned := map[string]bool{}
	for _, tool := range category.TargetTools {
		planned[tool] = true
	}
	bridged := []string{
		GitHubToolIssueList, GitHubToolIssueView, GitHubToolIssueCreate, GitHubToolIssueComment,
		GitHubToolPRList, GitHubToolPRView, GitHubToolPRCreate, GitHubToolPRComment, GitHubToolPRReview,
		GitHubToolPRMerge, GitHubToolAuthStatus, GitHubToolAPI,
	}
	for _, tool := range bridged {
		if !planned[tool] {
			t.Fatalf("bridge targets %s, which is not in the plan's %s target surface", tool, BashCategoryGitHubCLI)
		}
	}
	if len(bridged) != len(category.TargetTools) {
		t.Fatalf("the bridge covers %d tools but the plan lists %d: the bridge and the plan drifted apart",
			len(bridged), len(category.TargetTools))
	}
}

func TestMeasureGitHubShellTranslation(t *testing.T) {
	samples := []BashCallSample{
		{Tool: "coder_bash", Arguments: `{"command":"gh issue view 514 --repo msgmate-io/open-chat-go-ci --json body"}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh pr view 873 --json files,statusCheckRollup"}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh pr diff 873"}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh auth status"}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh api repos/msgmate-io/open-chat-go-ci/pulls/873/reviews"}`},
		{Tool: "coder_bash", Arguments: `{"command":"go test ./backend/nativecoder/..."}`},
		{Tool: "coder_bash", Arguments: `{"commands":["gh issue list --json number","git status"]}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh pr merge 873 --squash"}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh auth token"}`},
		{Tool: "coder_bash", Arguments: `""`},
	}
	report := MeasureGitHubShellTranslation(samples)

	if report.PlanVersion != BashRetirementPlanVersion {
		t.Fatalf("plan version = %d", report.PlanVersion)
	}
	if report.SourceTool != BashRetirementDeprecatedTool {
		t.Fatalf("source tool = %s", report.SourceTool)
	}
	if report.TotalCalls != len(samples) {
		t.Fatalf("total calls = %d, want %d", report.TotalCalls, len(samples))
	}
	if report.ErroredCalls != 1 {
		t.Fatalf("errored calls = %d, want 1", report.ErroredCalls)
	}
	if report.GitHubCommands != 8 {
		t.Fatalf("github commands = %d, want 8", report.GitHubCommands)
	}
	if report.TranslatedCommands != 7 {
		t.Fatalf("translated commands = %d, want 7", report.TranslatedCommands)
	}
	if report.ResidualCommands != 1 {
		t.Fatalf("residual commands = %d, want 1", report.ResidualCommands)
	}
	if report.ResidualReasons["`gh auth token` touches credentials; it is never bridged and stays on the guarded residual"] != 1 {
		t.Fatalf("residual reasons = %v", report.ResidualReasons)
	}
	if len(report.ResidualExamples) != 1 || report.ResidualExamples[0] != "gh auth token" {
		t.Fatalf("residual examples = %v", report.ResidualExamples)
	}
	if report.PhaseReady {
		t.Fatal("phase 1 must not be ready while a github command still has no typed equivalent")
	}
	if report.TranslationRatio <= 0.8 || report.TranslationRatio >= 1 {
		t.Fatalf("translation ratio = %v, want (0.8, 1)", report.TranslationRatio)
	}
}

func TestMeasureGitHubShellTranslationClearsThePhaseWhenGithubWorkIsGone(t *testing.T) {
	samples := []BashCallSample{
		{Tool: "coder_bash", Arguments: `{"command":"go build ./backend/..."}`},
	}
	report := MeasureGitHubShellTranslation(samples)
	if report.GitHubCommands != 0 {
		t.Fatalf("github commands = %d, want 0", report.GitHubCommands)
	}
	if !report.PhaseReady {
		t.Fatal("a shell record with no github_cli usage must clear the phase 1 exit criterion")
	}
	if report.ResidualReasons != nil {
		t.Fatalf("an empty residual map must serialise as absent, got %v", report.ResidualReasons)
	}
}

func TestMeasureGitHubShellTranslationEmpty(t *testing.T) {
	report := MeasureGitHubShellTranslation(nil)
	if report.TotalCalls != 0 || report.Commands != 0 {
		t.Fatalf("empty report = %+v", report)
	}
	if report.PhaseReady {
		t.Fatal("an empty sample set proves nothing and must not clear the phase")
	}
}

func TestTranslateGitHubShellCommandIsCaseSensitiveForTheBinary(t *testing.T) {
	// `GH ...` is not the gh CLI; the bridge must not claim it.
	if translation := TranslateGitHubShellCommand("GH issue list"); translation.Translated {
		t.Fatalf("unexpected translation: %+v", translation.Call)
	}
}

func TestGitHubShellTranslationReportJSONIsStable(t *testing.T) {
	report := MeasureGitHubShellTranslation([]BashCallSample{
		{Tool: "coder_bash", Arguments: `{"command":"gh issue list --json number"}`},
	})
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"plan_version"`, `"translation_ratio"`, `"phase_ready"`} {
		if !strings.Contains(string(payload), key) {
			t.Fatalf("report JSON missing %s: %s", key, payload)
		}
	}
}
