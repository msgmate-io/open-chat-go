package msgmate

import (
	"strings"
	"testing"

	"backend/nativecoder"
)

// TestCoderWorkspaceSystemPromptAppendsForCoder pins the ci#819 part A prompt
// half: a native coder config gets the workspace-root guidance appended, so the
// model learns the root up front instead of prefixing every `coder_bash` call
// with a redundant `cd`.
func TestCoderWorkspaceSystemPromptAppendsForCoder(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash", "coder_read"}}

	got := coderWorkspaceSystemPrompt("You are msgmate coder.", config)

	if !strings.HasPrefix(got, "You are msgmate coder.") {
		t.Fatalf("configured prompt must be preserved first, got %q", got)
	}
	if !strings.Contains(got, nativecoder.WorkspaceRootDefault) {
		t.Fatalf("guidance must name the workspace root %q, got %q", nativecoder.WorkspaceRootDefault, got)
	}
	if !strings.Contains(got, "Workspace root:") {
		t.Fatalf("guidance marker missing from %q", got)
	}
	if !strings.Contains(got, "git rev-parse --show-toplevel") {
		t.Fatalf("guidance must retire the git-rev-parse prefix, got %q", got)
	}
}

// TestCoderWorkspaceSystemPromptLeavesNonCoderAlone guards the shared handler
// path: a config that is not a native coder agent must be byte-identical, or the
// wiring would change the system prompt of every bot on the platform.
func TestCoderWorkspaceSystemPromptLeavesNonCoderAlone(t *testing.T) {
	for name, config := range map[string]map[string]interface{}{
		"nil":       nil,
		"empty":     {},
		"chat-only": {"tools": []interface{}{"chat_send_message"}},
	} {
		t.Run(name, func(t *testing.T) {
			const prompt = "You are a helpful assistant."
			if got := coderWorkspaceSystemPrompt(prompt, config); got != prompt {
				t.Fatalf("non-coder prompt must be untouched, got %q", got)
			}
		})
	}
}

// TestCoderWorkspaceSystemPromptIsIdempotent keeps the append from charging the
// model twice for guidance a config already baked into its prompt.
func TestCoderWorkspaceSystemPromptIsIdempotent(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash"}}

	once := coderWorkspaceSystemPrompt("You are msgmate coder.", config)
	twice := coderWorkspaceSystemPrompt(once, config)

	if twice != once {
		t.Fatalf("guidance emitted twice:\nfirst: %q\nsecond: %q", once, twice)
	}
}

// TestCoderWorkspaceSystemPromptNamesDeclaredRoot covers the non-default sandbox:
// a config that declares coder_workspace_root must be told that path, because the
// session layer applies the declared root, not the default.
func TestCoderWorkspaceSystemPromptNamesDeclaredRoot(t *testing.T) {
	const root = "/mnt/sandbox/work"
	config := map[string]interface{}{
		"tools":                            []interface{}{"coder_bash"},
		nativecoder.WorkspaceRootConfigKey: root,
	}

	got := coderWorkspaceSystemPrompt("You are msgmate coder.", config)

	if !strings.Contains(got, root) {
		t.Fatalf("guidance must name the declared root %q, got %q", root, got)
	}
	if strings.Contains(got, "- `"+nativecoder.WorkspaceRootDefault+"` is the root") {
		t.Fatalf("guidance must not fall back to the default root when one is declared, got %q", got)
	}
}

// TestCoderWorkspaceSystemPromptEmptyPrompt covers a coder config with no
// configured system prompt: the guidance alone must still be well formed (no
// leading blank lines, no padding).
func TestCoderWorkspaceSystemPromptEmptyPrompt(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash"}}

	got := coderWorkspaceSystemPrompt("   ", config)

	if got != nativecoder.GitTypedFirstAppend(nativecoder.GitHubTypedFirstAppend(nativecoder.WorkspaceRootGuidance())) {
		t.Fatalf("got %q, want the composed guidance alone", got)
	}
	if strings.HasPrefix(got, "\n") || strings.HasSuffix(got, "\n") {
		t.Fatalf("composed guidance must not be padded, got %q", got)
	}
}

// TestCoderWorkspaceSystemPromptIsTypedFirstGitHub pins the ci#514 phase 1
// prompt half: the prompt a native coder run really uses must prefer the typed
// GitHub tools, which is the evidence the phase-1 criterion is measured from.
// The assertion goes through the same measurement the criteria evaluator uses,
// so this test fails if the wiring is dropped.
func TestCoderWorkspaceSystemPromptIsTypedFirstGitHub(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash", "coder_read"}}

	got := coderWorkspaceSystemPrompt("You are msgmate coder.", config)

	if !strings.Contains(got, nativecoder.GitHubTypedFirstPromptMarker) {
		t.Fatalf("typed-first GitHub block missing from %q", got)
	}
	measured := nativecoder.MeasureGitHubTypedFirstPrompt(got)
	if !measured.Ready {
		t.Fatalf("rendered coder prompt is not typed-first ready: missing %v", measured.Missing)
	}
	for _, tool := range []string{"github_pr_view", "github_pr_create", "github_issue_comment"} {
		if !strings.Contains(got, tool) {
			t.Errorf("prompt must name the typed tool %q, got %q", tool, got)
		}
	}
}

// TestCoderWorkspaceSystemPromptPreBakedRootStillTypedFirst covers a config that
// already carries the workspace-root guidance: the root half must not be emitted
// twice, but the typed-first half must still be composed, because a pre-baked
// prompt is exactly the case that would otherwise stay typed-blind. The result
// must also stay stable across repeated composition.
func TestCoderWorkspaceSystemPromptPreBakedRootStillTypedFirst(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash"}}
	prebaked := "You are msgmate coder.\n\n" + nativecoder.WorkspaceRootGuidance()

	got := coderWorkspaceSystemPrompt(prebaked, config)

	if strings.Count(got, workspaceRootPromptMarker) != 1 {
		t.Fatalf("workspace-root guidance emitted more than once: %q", got)
	}
	if !nativecoder.MeasureGitHubTypedFirstPrompt(got).Ready {
		t.Fatalf("pre-baked prompt did not become typed-first: %q", got)
	}
	if again := coderWorkspaceSystemPrompt(got, config); again != got {
		t.Fatalf("composition is not stable: %q -> %q", got, again)
	}
}
