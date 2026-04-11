package nativecoder

import (
	"reflect"
	"testing"
)

func autoSandboxConfig(repo string, enabled interface{}) map[string]interface{} {
	workspace := map[string]interface{}{
		"mode":       "new_sandbox",
		"git_branch": "main",
	}
	if enabled != nil {
		workspace[AutoSandboxFlagInput] = enabled
	}
	if repo != "" {
		workspace[AutoSandboxGitRepoInput] = repo
	}
	return map[string]interface{}{
		"model": "test-model",
		"tool_init": map[string]interface{}{
			AutoSandboxToolInitKey: workspace,
		},
	}
}

func autoSandboxToolInit(repo string, enabled interface{}) map[string]interface{} {
	return autoSandboxConfig(repo, enabled)["tool_init"].(map[string]interface{})
}

func autoSandboxRepo(t *testing.T, config map[string]interface{}) string {
	t.Helper()
	toolInit, ok := config["tool_init"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool_init missing after binding: %#v", config)
	}
	workspace, ok := toolInit[AutoSandboxToolInitKey].(map[string]interface{})
	if !ok {
		t.Fatalf("workspace init missing after binding: %#v", toolInit)
	}
	value, _ := workspace[AutoSandboxGitRepoInput].(string)
	return value
}

func TestRepoValueFromTags(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want string
	}{
		{name: "no tags", tags: nil, want: ""},
		{name: "unrelated tags", tags: []string{"issue:413", "bot:coder"}, want: ""},
		{name: "explicit repo slug", tags: []string{"issue:413", "repo:msgmate-io/open-chat-go"}, want: "msgmate-io/open-chat-go"},
		{name: "case insensitive prefix", tags: []string{"REPO:owner/name"}, want: "owner/name"},
		{name: "whitespace tolerant", tags: []string{" repo: owner/name "}, want: "owner/name"},
		{name: "empty repo value ignored", tags: []string{"repo:", "repo:owner/name"}, want: "owner/name"},
		{name: "first repo tag wins", tags: []string{"repo:a/b", "repo:c/d"}, want: "a/b"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := RepoValueFromTags(testCase.tags); got != testCase.want {
				t.Fatalf("RepoValueFromTags(%v) = %q, want %q", testCase.tags, got, testCase.want)
			}
		})
	}
}

func TestGitRemoteForRepo(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "single segment is not a repo", in: "open-chat-go-ci", want: ""},
		{name: "owner/name slug", in: "msgmate-io/open-chat-go", want: "https://github.com/msgmate-io/open-chat-go.git"},
		{name: "https remote passes through", in: "https://github.com/msgmate-io/open-chat-go.git", want: "https://github.com/msgmate-io/open-chat-go.git"},
		{name: "ssh scp remote passes through", in: "git@github.com:msgmate-io/open-chat-go.git", want: "git@github.com:msgmate-io/open-chat-go.git"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := GitRemoteForRepo(testCase.in); got != testCase.want {
				t.Fatalf("GitRemoteForRepo(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}

func TestAutoSandboxToolInitRepo(t *testing.T) {
	if got := AutoSandboxToolInitRepo(nil); got != "" {
		t.Fatalf("nil tool_init should report no repo, got %q", got)
	}
	if got := AutoSandboxToolInitRepo(map[string]interface{}{"coder_git": map[string]interface{}{}}); got != "" {
		t.Fatalf("unrelated tool_init should report no repo, got %q", got)
	}
	if got := AutoSandboxToolInitRepo(autoSandboxToolInit("", true)); got != "" {
		t.Fatalf("sandbox init without git_repo should report no repo, got %q", got)
	}
	if got := AutoSandboxToolInitRepo(autoSandboxToolInit(" owner/name ", true)); got != "owner/name" {
		t.Fatalf("explicit git_repo should be reported trimmed, got %q", got)
	}
}

func TestExplicitAutoSandboxRepo(t *testing.T) {
	if got := ExplicitAutoSandboxRepo(nil, nil); got != "" {
		t.Fatalf("nothing pinned should be empty, got %q", got)
	}
	if got := ExplicitAutoSandboxRepo(autoSandboxToolInit("owner/name", true), nil); got != "owner/name" {
		t.Fatalf("tool_init pin should win, got %q", got)
	}
	overrides := map[string]interface{}{"repo": "override-owner/override-repo"}
	if got := ExplicitAutoSandboxRepo(autoSandboxToolInit("owner/name", true), overrides); got != "owner/name" {
		t.Fatalf("tool_init pin should beat config override, got %q", got)
	}
	if got := ExplicitAutoSandboxRepo(nil, overrides); got != "override-owner/override-repo" {
		t.Fatalf("config override pin should be used, got %q", got)
	}
}

func TestAutoSandboxRepoTargetPrefersTagOverConfig(t *testing.T) {
	config := map[string]interface{}{"repo": "config-owner/config-repo"}
	if got := AutoSandboxRepoTarget(config, []string{"repo:tag-owner/tag-repo"}); got != "tag-owner/tag-repo" {
		t.Fatalf("explicit tag should win, got %q", got)
	}
	if got := AutoSandboxRepoTarget(config, nil); got != "config-owner/config-repo" {
		t.Fatalf("config repo should be used without a tag, got %q", got)
	}
	if got := AutoSandboxRepoTarget(map[string]interface{}{}, nil); got != "" {
		t.Fatalf("no repo should resolve to empty, got %q", got)
	}
}

func TestAutoSandboxRepoForInteraction(t *testing.T) {
	config := map[string]interface{}{"repo": "config-owner/config-repo"}

	if got := AutoSandboxRepoForInteraction(config, []string{"repo:tag-owner/tag-repo"}, nil, nil); got != "tag-owner/tag-repo" {
		t.Fatalf("tag should win, got %q", got)
	}
	if got := AutoSandboxRepoForInteraction(config, nil, nil, nil); got != "config-owner/config-repo" {
		t.Fatalf("config default should be used, got %q", got)
	}
	// a pinned sandbox repository is authoritative, even against a dispatch tag
	if got := AutoSandboxRepoForInteraction(config, []string{"repo:tag-owner/tag-repo"}, autoSandboxToolInit("pinned-owner/pinned-repo", true), nil); got != "" {
		t.Fatalf("pinned tool_init repo must not be re-bound, got %q", got)
	}
	if got := AutoSandboxRepoForInteraction(config, nil, nil, map[string]interface{}{"repo": "pinned-owner/pinned-repo"}); got != "" {
		t.Fatalf("pinned override repo must not be re-bound, got %q", got)
	}
}

func TestBindAutoSandboxRepoBindsRequestedRepository(t *testing.T) {
	config := autoSandboxConfig("", true)
	bound, changed := BindAutoSandboxRepo(config["tool_init"].(map[string]interface{}), "msgmate-io/open-chat-go-ci")
	if !changed {
		t.Fatal("expected the binding to report a change")
	}
	workspace := bound[AutoSandboxToolInitKey].(map[string]interface{})
	if workspace[AutoSandboxGitRepoInput] != "https://github.com/msgmate-io/open-chat-go-ci.git" {
		t.Fatalf("unexpected git_repo: %#v", workspace[AutoSandboxGitRepoInput])
	}
	// unchanged inputs must survive the copy
	if workspace["mode"] != "new_sandbox" || workspace["git_branch"] != "main" {
		t.Fatalf("existing inputs were dropped: %#v", workspace)
	}
	// the caller's config must not be mutated
	original := config["tool_init"].(map[string]interface{})[AutoSandboxToolInitKey].(map[string]interface{})
	if _, ok := original[AutoSandboxGitRepoInput]; ok {
		t.Fatalf("caller config was mutated: %#v", original)
	}
	// binding the same repository again is a no-op (idempotent)
	if _, changed := BindAutoSandboxRepo(bound, "msgmate-io/open-chat-go-ci"); changed {
		t.Fatal("re-binding the same repository should not report a change")
	}
}

func TestBindAutoSandboxRepoReplacesBotDefaultCheckout(t *testing.T) {
	toolInit := autoSandboxToolInit("https://github.com/msgmate-io/open-chat-go.git", true)
	bound, changed := BindAutoSandboxRepo(toolInit, "msgmate-io/open-chat-go-ci")
	if !changed {
		t.Fatal("expected the bot-default checkout to be replaced")
	}
	if got := AutoSandboxToolInitRepo(bound); got != "https://github.com/msgmate-io/open-chat-go-ci.git" {
		t.Fatalf("git_repo = %q", got)
	}
	// the caller's map is untouched
	if got := AutoSandboxToolInitRepo(toolInit); got != "https://github.com/msgmate-io/open-chat-go.git" {
		t.Fatalf("caller tool_init was mutated: %q", got)
	}
}

func TestBindAutoSandboxRepoLeavesConfigUntouched(t *testing.T) {
	cases := []struct {
		name     string
		toolInit map[string]interface{}
		repo     string
	}{
		{name: "nil tool_init", toolInit: nil, repo: "owner/name"},
		{name: "no workspace init", toolInit: map[string]interface{}{"coder_git": map[string]interface{}{}}, repo: "owner/name"},
		{name: "auto_sandbox off", toolInit: autoSandboxToolInit("", false), repo: "owner/name"},
		{name: "auto_sandbox on as string", toolInit: autoSandboxToolInit("", "no"), repo: "owner/name"},
		{name: "auto_sandbox missing", toolInit: autoSandboxToolInit("", nil), repo: "owner/name"},
		{name: "unusable repo value", toolInit: autoSandboxToolInit("", true), repo: "single-segment"},
		{name: "empty repo value", toolInit: autoSandboxToolInit("", true), repo: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, changed := BindAutoSandboxRepo(testCase.toolInit, testCase.repo)
			if changed {
				t.Fatalf("expected no change, got %#v", got)
			}
			if !reflect.DeepEqual(got, testCase.toolInit) {
				t.Fatalf("tool_init was replaced: %#v", got)
			}
		})
	}
}

func TestWithAutoSandboxRepoConfigLevel(t *testing.T) {
	config := autoSandboxConfig("", "true")
	bound := WithAutoSandboxRepo(config, "msgmate-io/open-chat-go")
	if got, want := autoSandboxRepo(t, bound), "https://github.com/msgmate-io/open-chat-go.git"; got != want {
		t.Fatalf("git_repo = %q, want %q", got, want)
	}
	// unrelated config keys are preserved
	if bound["model"] != "test-model" {
		t.Fatalf("unrelated config key lost: %#v", bound)
	}
	// an unusable repo value must not add a git_repo input
	if repo := autoSandboxRepo(t, WithAutoSandboxRepo(config, "single-segment")); repo != "" {
		t.Fatalf("unusable repo should not bind, got %q", repo)
	}
	// configs without tool_init are returned as-is
	plain := map[string]interface{}{"model": "test-model"}
	if got := WithAutoSandboxRepo(plain, "msgmate-io/open-chat-go"); !reflect.DeepEqual(got, plain) {
		t.Fatalf("config without tool_init changed: %#v", got)
	}
	if WithAutoSandboxRepo(nil, "msgmate-io/open-chat-go") != nil {
		t.Fatal("nil config should stay nil")
	}
}

func TestBranchValueFromTags(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want string
	}{
		{name: "no tags", tags: nil, want: ""},
		{name: "unrelated tags", tags: []string{"repo:owner/name", "issue:413"}, want: ""},
		{name: "explicit branch", tags: []string{"issue:413", "branch:feat/x"}, want: "feat/x"},
		{name: "case insensitive prefix", tags: []string{"BRANCH:Feat/X"}, want: "Feat/X"},
		{name: "whitespace tolerant", tags: []string{" branch: feat/x "}, want: "feat/x"},
		{name: "empty branch value ignored", tags: []string{"branch:", "branch:feat/x"}, want: "feat/x"},
		{name: "first branch tag wins", tags: []string{"branch:one", "branch:two"}, want: "one"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := BranchValueFromTags(testCase.tags); got != testCase.want {
				t.Fatalf("BranchValueFromTags(%v) = %q, want %q", testCase.tags, got, testCase.want)
			}
		})
	}
}

func TestAutoSandboxBranchForInteraction(t *testing.T) {
	// an explicit `branch:<name>` tag is re-bound into the sandbox init
	if got := AutoSandboxBranchForInteraction([]string{"branch:Feat/X"}, nil); got != "Feat/X" {
		t.Fatalf("tag branch should be selected, got %q", got)
	}
	// a branch the caller pinned in tool_init is never re-bound
	pinned := map[string]interface{}{
		AutoSandboxToolInitKey: map[string]interface{}{
			AutoSandboxFlagInput:      true,
			AutoSandboxGitBranchInput: "release/1.2",
		},
	}
	if got := AutoSandboxBranchForInteraction([]string{"branch:Feat/X"}, pinned); got != "" {
		t.Fatalf("pinned branch should not be re-bound, got %q", got)
	}
	if got := AutoSandboxBranchForInteraction(nil, pinned); got != "" {
		t.Fatalf("pinned branch alone should select nothing, got %q", got)
	}
	if got := AutoSandboxBranchForInteraction(nil, nil); got != "" {
		t.Fatalf("no tag and no pin should select nothing, got %q", got)
	}
}

func TestBindAutoSandboxBranch(t *testing.T) {
	toolInit := autoSandboxToolInit("msgmate-io/open-chat-go", true)
	bound, changed := BindAutoSandboxBranch(toolInit, "refs/heads/Feat/X")
	if !changed {
		t.Fatal("expected the branch to be bound")
	}
	workspace := bound[AutoSandboxToolInitKey].(map[string]interface{})
	if got := workspace[AutoSandboxGitBranchInput]; got != "Feat/X" {
		t.Fatalf("git_branch = %#v, want the case-preserving branch", got)
	}
	// the repository input and the other inputs survive
	if got := workspace[AutoSandboxGitRepoInput]; got != "msgmate-io/open-chat-go" {
		t.Fatalf("git_repo = %#v", got)
	}
	if workspace[AutoSandboxFlagInput] != true {
		t.Fatalf("auto_sandbox flag lost: %#v", workspace)
	}
	// the caller's map is untouched
	if got := autoSandboxToolInitBranch(toolInit); got != "main" {
		t.Fatalf("caller tool_init was mutated: %q", got)
	}
	// binding the same branch again is a no-op (idempotent)
	if _, changed := BindAutoSandboxBranch(bound, "Feat/X"); changed {
		t.Fatal("re-binding the same branch should not report a change")
	}
}

func TestBindAutoSandboxBranchLeavesConfigUntouched(t *testing.T) {
	cases := []struct {
		name     string
		toolInit map[string]interface{}
		branch   string
	}{
		{name: "nil tool_init", toolInit: nil, branch: "feat/x"},
		{name: "no workspace init", toolInit: map[string]interface{}{"coder_git": map[string]interface{}{}}, branch: "feat/x"},
		{name: "auto_sandbox off", toolInit: autoSandboxToolInit("", false), branch: "feat/x"},
		{name: "auto_sandbox on as string", toolInit: autoSandboxToolInit("", "no"), branch: "feat/x"},
		{name: "auto_sandbox missing", toolInit: autoSandboxToolInit("", nil), branch: "feat/x"},
		{name: "empty branch value", toolInit: autoSandboxToolInit("", true), branch: ""},
		{name: "refs/heads only", toolInit: autoSandboxToolInit("", true), branch: "refs/heads/"},
		{name: "whitespace inside branch", toolInit: autoSandboxToolInit("", true), branch: "feat x"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, changed := BindAutoSandboxBranch(testCase.toolInit, testCase.branch)
			if changed {
				t.Fatalf("expected no change, got %#v", got)
			}
			if !reflect.DeepEqual(got, testCase.toolInit) {
				t.Fatalf("tool_init was replaced: %#v", got)
			}
		})
	}
}

func TestWithAutoSandboxBranchConfigLevel(t *testing.T) {
	config := autoSandboxConfig("msgmate-io/open-chat-go", "true")
	bound := WithAutoSandboxBranch(config, "Feat/X")
	workspace := bound["tool_init"].(map[string]interface{})[AutoSandboxToolInitKey].(map[string]interface{})
	if got := workspace[AutoSandboxGitBranchInput]; got != "Feat/X" {
		t.Fatalf("git_branch = %#v, want %q", got, "Feat/X")
	}
	// unrelated config keys are preserved and the original is untouched
	if bound["model"] != "test-model" {
		t.Fatalf("unrelated config key lost: %#v", bound)
	}
	if got := autoSandboxToolInitBranch(config["tool_init"].(map[string]interface{})); got != "main" {
		t.Fatalf("caller config was mutated: %q", got)
	}
	// configs without tool_init are returned as-is
	plain := map[string]interface{}{"model": "test-model"}
	if got := WithAutoSandboxBranch(plain, "Feat/X"); !reflect.DeepEqual(got, plain) {
		t.Fatalf("config without tool_init changed: %#v", got)
	}
	if WithAutoSandboxBranch(nil, "Feat/X") != nil {
		t.Fatal("nil config should stay nil")
	}
}

// autoSandboxToolInitBranch reads git_branch out of a tool_init fixture.
func autoSandboxToolInitBranch(toolInit map[string]interface{}) string {
	workspaceInit, ok := toolInit[AutoSandboxToolInitKey].(map[string]interface{})
	if !ok {
		return ""
	}
	value, _ := workspaceInit[AutoSandboxGitBranchInput].(string)
	return value
}
