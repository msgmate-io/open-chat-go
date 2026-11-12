package agentdefaults

import "testing"

// ci#687 / ci#622: a bot's stored workspace pin must not override the task repo
// the dispatch names - otherwise the sandbox is provisioned with the wrong
// checkout and the role works on (or gives up on) the wrong repository.

func TestRepoSlugNormalisation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain slug", "msgmate-io/open-chat-go", "msgmate-io/open-chat-go"},
		{"https", "https://github.com/msgmate-io/open-chat-go", "msgmate-io/open-chat-go"},
		{"https dot git", "https://github.com/msgmate-io/open-chat-go.git", "msgmate-io/open-chat-go"},
		{"trailing slash", "https://github.com/msgmate-io/open-chat-go/", "msgmate-io/open-chat-go"},
		{"scp style", "git@github.com:msgmate-io/open-chat-go.git", "msgmate-io/open-chat-go"},
		{"case folded", "MsgMate-IO/Open-Chat-Go", "msgmate-io/open-chat-go"},
		{"empty", "   ", ""},
		{"no owner", "open-chat-go", ""},
		{"too deep", "a/b/c", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RepoSlug(tc.in); got != tc.want {
				t.Fatalf("RepoSlug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTaskRepoFromTags(t *testing.T) {
	tags := []string{"bot:msgmate-coder-development-lead", " repo:MsgMate-IO/open-chat-go "}
	if got := TaskRepoFromTags(tags); got != "msgmate-io/open-chat-go" {
		t.Fatalf("TaskRepoFromTags = %q, want msgmate-io/open-chat-go", got)
	}
	if got := TaskRepoFromTags([]string{"ci", "branch:dev"}); got != "" {
		t.Fatalf("TaskRepoFromTags without repo tag = %q, want empty", got)
	}
	if got := TaskRepoFromTags([]string{"repo:"}); got != "" {
		t.Fatalf("TaskRepoFromTags with empty repo tag = %q, want empty", got)
	}
}

// storedPinnedConfig models a role bot's default_shared_config: it carries a
// stored workspace pin that names the monorepo the bot was configured with,
// not the task repo of a single dispatch.
func storedPinnedConfig(repo string) map[string]interface{} {
	return map[string]interface{}{
		ToolsConfigKey: []interface{}{"coder_bash", WorkspaceSelectorTool},
		ToolInitConfigKey: map[string]interface{}{
			WorkspaceSelectorTool: map[string]interface{}{
				WorkspaceRepoConfigKey: repo,
				"auto_sandbox":         true,
			},
		},
	}
}

func pinnedRepoOf(t *testing.T, config map[string]interface{}) string {
	t.Helper()
	return pinnedWorkspaceRepo(config)
}

func TestReconcileTaskRepoReplacesContradictingPin(t *testing.T) {
	config := storedPinnedConfig("https://github.com/msgmate-io/msgmate-coder-mcp")
	reconciled := ReconcileTaskRepo(config, []string{"repo:msgmate-io/open-chat-go"}, nil)

	if got, want := pinnedRepoOf(t, reconciled), "https://github.com/msgmate-io/open-chat-go"; got != want {
		t.Fatalf("reconciled pin = %q, want %q", got, want)
	}
	// the sibling keys of the selector survive
	selector := reconciled[ToolInitConfigKey].(map[string]interface{})[WorkspaceSelectorTool].(map[string]interface{})
	if selector["auto_sandbox"] != true {
		t.Fatalf("reconciliation dropped sibling selector keys: %#v", selector)
	}
	// the input config is never mutated
	if got := pinnedRepoOf(t, config); got != "https://github.com/msgmate-io/msgmate-coder-mcp" {
		t.Fatalf("input config was mutated: pin = %q", got)
	}
}

func TestReconcileTaskRepoNoOpCases(t *testing.T) {
	taskTags := []string{"repo:msgmate-io/open-chat-go"}

	t.Run("no task repo tag", func(t *testing.T) {
		config := storedPinnedConfig("https://github.com/msgmate-io/msgmate-coder-mcp")
		if got := pinnedRepoOf(t, ReconcileTaskRepo(config, []string{"branch:dev"}, nil)); got != "https://github.com/msgmate-io/msgmate-coder-mcp" {
			t.Fatalf("pin changed without a task repo: %q", got)
		}
	})

	t.Run("pin already the task repo", func(t *testing.T) {
		config := storedPinnedConfig("https://github.com/msgmate-io/open-chat-go.git")
		if got := pinnedRepoOf(t, ReconcileTaskRepo(config, taskTags, nil)); got != "https://github.com/msgmate-io/open-chat-go.git" {
			t.Fatalf("matching pin was rewritten: %q", got)
		}
	})

	t.Run("no stored pin", func(t *testing.T) {
		config := map[string]interface{}{ToolsConfigKey: []interface{}{WorkspaceSelectorTool}}
		if got := ReconcileTaskRepo(config, taskTags, nil); len(got) != 1 {
			t.Fatalf("config without tool_init was rewritten: %#v", got)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		if got := ReconcileTaskRepo(nil, taskTags, nil); got != nil {
			t.Fatalf("nil config returned %#v", got)
		}
	})

	// ci#622 follow-up: a caller-pinned selection stays authoritative.
	t.Run("caller pinned workspace explicitly", func(t *testing.T) {
		config := storedPinnedConfig("https://github.com/msgmate-io/msgmate-coder-mcp")
		explicit := map[string]interface{}{
			WorkspaceSelectorTool: map[string]interface{}{
				WorkspaceRepoConfigKey: "https://github.com/msgmate-io/other-repo",
			},
		}
		if got := pinnedRepoOf(t, ReconcileTaskRepo(config, taskTags, explicit)); got != "https://github.com/msgmate-io/msgmate-coder-mcp" {
			t.Fatalf("explicit caller pin was overridden: %q", got)
		}
	})
}

// ci#622: the rewrite keeps the host the stored pin was written with, so a
// non-github or ssh-scp workspace selection is not silently moved.
func TestReconcileTaskRepoKeepsConfiguredHost(t *testing.T) {
	taskTags := []string{"repo:msgmate-io/open-chat-go"}

	cases := []struct {
		name   string
		stored string
		want   string
	}{
		{"https", "https://github.com/msgmate-io/msgmate-coder-mcp", "https://github.com/msgmate-io/open-chat-go"},
		{"scp like", "git@github.com:msgmate-io/msgmate-coder-mcp.git", "git@github.com:msgmate-io/open-chat-go"},
		{"bare slug", "msgmate-io/msgmate-coder-mcp", "https://github.com/msgmate-io/open-chat-go"},
		{"non github host", "https://gitlab.com/other/repo.git", "https://gitlab.com/msgmate-io/open-chat-go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := storedPinnedConfig(tc.stored)
			if got := pinnedRepoOf(t, ReconcileTaskRepo(config, taskTags, nil)); got != tc.want {
				t.Fatalf("rewritten pin = %q, want %q", got, tc.want)
			}
		})
	}
}
