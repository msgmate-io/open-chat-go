package nativecoder

import (
	"errors"
	"testing"
)

// ci#819 part B: the chat-start pre-call hook satisfies an init-requiring tool
// (coder_select_workspace) before the agent's first turn, instead of relying on
// the model to call the initialiser itself.
func TestPreCallInitSeedsWorkspaceSelectionFromTags(t *testing.T) {
	ctx := PreCallInitContext{
		Tools: []string{"coder_bash", coderToolInitSelectWorkspace},
		Tags:  []string{"issue:819", "repo:msgmate-io/open-chat-go", "branch:main"},
	}
	result := RunPreCallInit(ctx, DefaultPreCallInitResolvers())

	seeded, ok := result.Seeded[coderToolInitSelectWorkspace]
	if !ok {
		t.Fatalf("workspace selection was not seeded: %#v", result)
	}
	if got := seeded[AutoSandboxGitRepoInput]; got != "msgmate-io/open-chat-go" {
		t.Fatalf("git_repo = %#v", got)
	}
	if got := seeded[AutoSandboxGitBranchInput]; got != "main" {
		t.Fatalf("git_branch = %#v", got)
	}
	if got := seeded[AutoSandboxFlagInput]; got != true {
		t.Fatalf("auto_sandbox = %#v, want true", got)
	}
	if len(result.Applied) != 1 || result.Applied[0] != coderToolInitSelectWorkspace {
		t.Fatalf("applied = %#v", result.Applied)
	}
	if _, skipped := result.Skipped[coderToolInitSelectWorkspace]; skipped {
		t.Fatalf("seeded tool reported as skipped: %#v", result.Skipped)
	}
	// The hook must not mutate the caller's tag list or config.
	if ctx.Tags[1] != "repo:msgmate-io/open-chat-go" {
		t.Fatalf("input tags mutated: %#v", ctx.Tags)
	}
}

func TestPreCallInitRespectsExistingSelection(t *testing.T) {
	pinned := map[string]interface{}{AutoSandboxGitRepoInput: "pinned/owner-repo"}
	ctx := PreCallInitContext{
		Tools: []string{coderToolInitSelectWorkspace},
		Tags:  []string{"repo:dispatch/repo"},
		Init:  map[string]map[string]interface{}{coderToolInitSelectWorkspace: pinned},
	}
	result := RunPreCallInit(ctx, DefaultPreCallInitResolvers())
	if len(result.Seeded) != 0 {
		t.Fatalf("caller selection must win, seeded %#v", result.Seeded)
	}
	if result.Skipped[coderToolInitSelectWorkspace] == "" {
		t.Fatalf("expected a skip reason, got %#v", result)
	}
}

func TestPreCallInitLeavesUnresolvableToAgent(t *testing.T) {
	// No repo tag and no repo config: the hook has no default and must not seed
	// an empty selection that would mask the agent's own choice.
	ctx := PreCallInitContext{Tools: []string{coderToolInitSelectWorkspace}, Tags: []string{"issue:819"}}
	result := RunPreCallInit(ctx, DefaultPreCallInitResolvers())
	if len(result.Seeded) != 0 {
		t.Fatalf("nothing resolvable, seeded %#v", result.Seeded)
	}
	if result.Skipped[coderToolInitSelectWorkspace] != "no default resolvable" {
		t.Fatalf("skip reason = %q", result.Skipped[coderToolInitSelectWorkspace])
	}

	// A tool that is not configured on the chat is never seeded either.
	unconfigured := PreCallInitContext{Tools: []string{"coder_bash"}, Tags: []string{"repo:o/n"}}
	if got := RunPreCallInit(unconfigured, DefaultPreCallInitResolvers()); len(got.Seeded) != 0 {
		t.Fatalf("unconfigured tool seeded %#v", got.Seeded)
	}
}

// A resolver failure must degrade to a skip: a chat still has to start.
func TestRunPreCallInitNeverFailsTheChatStart(t *testing.T) {
	boom := InitResolver{Tool: "coder_thing", Resolve: func(PreCallInitContext) (map[string]interface{}, error) {
		return nil, errors.New("no default")
	}}
	result := RunPreCallInit(PreCallInitContext{Tools: []string{"coder_thing"}}, []InitResolver{boom})
	if len(result.Seeded) != 0 || result.Skipped["coder_thing"] != "no default" {
		t.Fatalf("resolver error not reported as a skip: %#v", result)
	}
}

func TestWithPreCallInitSeedsToolInit(t *testing.T) {
	config := map[string]interface{}{"model": "coder", "tool_init": map[string]interface{}{"coder_read": map[string]interface{}{"x": 1}}}
	out := WithPreCallInit(config, []string{"coder_bash", coderToolInitSelectWorkspace}, []string{"repo:msgmate-io/open-chat-go"}, DefaultPreCallInitResolvers())

	init, _ := out["tool_init"].(map[string]interface{})
	seeded, _ := init[coderToolInitSelectWorkspace].(map[string]interface{})
	if seeded[AutoSandboxGitRepoInput] != "msgmate-io/open-chat-go" || seeded[AutoSandboxFlagInput] != true {
		t.Fatalf("seeded workspace init = %#v", seeded)
	}
	if _, preserved := init["coder_read"]; !preserved {
		t.Fatalf("unrelated tool_init dropped: %#v", init)
	}
	// The original config is untouched.
	original, _ := config["tool_init"].(map[string]interface{})
	if _, leaked := original[coderToolInitSelectWorkspace]; leaked {
		t.Fatalf("input config mutated: %#v", config)
	}
}

func TestWithPreCallInitIsANoOpWhenNothingApplies(t *testing.T) {
	config := map[string]interface{}{"model": "coder"}
	if got := WithPreCallInit(config, []string{"coder_bash"}, []string{"repo:o/n"}, DefaultPreCallInitResolvers()); len(got) != 1 {
		t.Fatalf("unconfigured tool changed the config: %#v", got)
	}
	if got := WithPreCallInit(nil, []string{coderToolInitSelectWorkspace}, nil, DefaultPreCallInitResolvers()); got != nil {
		t.Fatalf("nil config with nothing derivable = %#v", got)
	}
}
