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

	if got != nativecoder.WorkspaceRootGuidance() {
		t.Fatalf("got %q, want the guidance alone", got)
	}
}
