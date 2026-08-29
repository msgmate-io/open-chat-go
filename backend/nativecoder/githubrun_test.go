package nativecoder

import "testing"

func TestGitHubRunToolConstants(t *testing.T) {
	if GitHubToolRunList != "github_run_list" || GitHubToolRunView != "github_run_view" {
		t.Fatalf("unexpected tool names: %q %q", GitHubToolRunList, GitHubToolRunView)
	}
}

func TestTranslateGitHubRunList(t *testing.T) {
	tests := []struct {
		cmd  string
		want GitHubRunListCall
	}{
		{`gh run list --repo msgmate-io/open-chat-go --limit 25`,
			GitHubRunListCall{Repo: "msgmate-io/open-chat-go", Limit: 25}},
		{`gh run list -R a/b --workflow ci.yml --branch main --status in_progress -L 5`,
			GitHubRunListCall{Repo: "a/b", Workflow: "ci.yml", Branch: "main", Status: "in_progress", Limit: 5}},
		{`gh run list`,
			GitHubRunListCall{Limit: 10}},
		{`GH_REPO=a/b gh run list -s completed`,
			GitHubRunListCall{Status: "completed", Limit: 10}},
	}
	for _, tc := range tests {
		tr, ok := TranslateGitHubRunCommand(tc.cmd)
		if !ok || tr.Tool != GitHubToolRunList {
			t.Fatalf("%q: want list tool, got ok=%v tool=%q", tc.cmd, ok, tr.Tool)
		}
		if tr.ListCall != tc.want {
			t.Fatalf("%q: got %+v want %+v", tc.cmd, tr.ListCall, tc.want)
		}
	}
}

func TestTranslateGitHubRunView(t *testing.T) {
	tests := []struct {
		cmd  string
		want GitHubRunViewCall
	}{
		{`gh run view 123 --repo a/b`,
			GitHubRunViewCall{RunID: "123", Repo: "a/b"}},
		{`gh run view latest -R a/b --json status,jobs`,
			GitHubRunViewCall{RunID: "latest", Repo: "a/b", JSON: true, JSONFields: "status,jobs"}},
		{`gh run view 55 --log-failed`,
			GitHubRunViewCall{RunID: "55", Failed: true}},
		{`gh run view`,
			GitHubRunViewCall{RunID: "latest"}},
	}
	for _, tc := range tests {
		tr, ok := TranslateGitHubRunCommand(tc.cmd)
		if !ok || tr.Tool != GitHubToolRunView {
			t.Fatalf("%q: want view tool, got ok=%v tool=%q", tc.cmd, ok, tr.Tool)
		}
		if tr.ViewCall != tc.want {
			t.Fatalf("%q: got %+v want %+v", tc.cmd, tr.ViewCall, tc.want)
		}
	}
}

func TestGitHubRunClassification(t *testing.T) {
	for _, cmd := range []string{`gh run list`, `GH_TOKEN=x gh run view 1`, `gh run list --repo a/b`} {
		if !IsGitHubRunCommand(cmd) {
			t.Fatalf("%q must classify as a gh run command", cmd)
		}
	}
	for _, cmd := range []string{`gh issue list`, `git log`, `gh`, `run list`} {
		if IsGitHubRunCommand(cmd) {
			t.Fatalf("%q must not classify as a gh run command", cmd)
		}
	}
	if got := GitHubRunToolName(`gh run list`); got != GitHubToolRunList {
		t.Fatalf("GitHubRunToolName(gh run list) = %q, want %q", got, GitHubToolRunList)
	}
	if got := GitHubRunToolName(`gh run rerun 123`); got != "" {
		t.Fatalf("GitHubRunToolName(gh run rerun) = %q, want empty", got)
	}
}

func TestGitHubRunResidual(t *testing.T) {
	for _, cmd := range []string{`gh run`, `gh run rerun 123`, `gh run download 123`, `gh run watch --exit-status 123`} {
		if _, ok := TranslateGitHubRunCommand(cmd); ok {
			t.Fatalf("%q must stay residual", cmd)
		}
	}
}

func TestMeasureGitHubRunTranslation(t *testing.T) {
	commands := []string{
		`gh run list`,
		`gh run view 1`,
		`gh run rerun 2`,
		`gh issue list`,
		`gh run`,
	}
	m := MeasureGitHubRunTranslation(commands)
	if m.Total != 4 || m.Expressible != 2 || len(m.Residual) != 2 {
		t.Fatalf("got %+v, want total=4 expressible=2 residual=2", m)
	}
}}
