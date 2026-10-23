package msgmate

import (
	"strings"
	"testing"

	"backend/nativecoder"
)

// TestCoderCompletionSystemPromptAppendsForCoder pins the ci#809 prompt half: a
// native coder whose resolved surface carries `all_tasks_completed` is told the
// tool is the machine-readable "I am done" signal, so it finishes concisely
// instead of relying only on the visible sentinel.
func TestCoderCompletionSystemPromptAppendsForCoder(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash", nativecoder.AllTasksCompletedToolName}}
	tools := []string{"coder_bash", nativecoder.AllTasksCompletedToolName}

	got := coderCompletionSystemPrompt("You are msgmate coder.", config, tools)

	if !strings.HasPrefix(got, "You are msgmate coder.") {
		t.Fatalf("configured prompt must be preserved first, got %q", got)
	}
	if !strings.Contains(got, "Task completion signal:") {
		t.Fatalf("guidance marker missing from %q", got)
	}
	if !strings.Contains(got, nativecoder.AllTasksCompletedToolName) {
		t.Fatalf("guidance must name the completion tool, got %q", got)
	}
	if want := nativecoder.CoderTaskCompletionGuidance(); !strings.Contains(got, want) {
		t.Fatalf("guidance must reuse the shared text, got %q", got)
	}
}

// TestCoderCompletionSystemPromptSilentWithoutTool is the important guard: the
// model must never be told to call a tool that is not on its resolved surface
// (an operator-narrowed tools list, or the deployment kill switch).
func TestCoderCompletionSystemPromptSilentWithoutTool(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash"}}
	const prompt = "You are msgmate coder."

	if got := coderCompletionSystemPrompt(prompt, config, []string{"coder_bash"}); got != prompt {
		t.Fatalf("prompt must stay untouched without the tool, got %q", got)
	}
}

// TestCoderCompletionSystemPromptLeavesNonCoderAlone keeps the shared handler
// path inert for every bot that is not a native coder agent.
func TestCoderCompletionSystemPromptLeavesNonCoderAlone(t *testing.T) {
	tools := []string{nativecoder.AllTasksCompletedToolName}
	for name, config := range map[string]map[string]interface{}{
		"nil":       nil,
		"empty":     {},
		"chat-only": {"tools": []interface{}{"chat_send_message"}},
	} {
		t.Run(name, func(t *testing.T) {
			const prompt = "You are a helpful assistant."
			if got := coderCompletionSystemPrompt(prompt, config, tools); got != prompt {
				t.Fatalf("non-coder prompt must be untouched, got %q", got)
			}
		})
	}
}

// TestCoderCompletionSystemPromptIsIdempotent keeps the append from charging the
// model twice for guidance a config already baked into its prompt.
func TestCoderCompletionSystemPromptIsIdempotent(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash", nativecoder.AllTasksCompletedToolName}}
	tools := []string{"coder_bash", nativecoder.AllTasksCompletedToolName}

	once := coderCompletionSystemPrompt("You are msgmate coder.", config, tools)
	twice := coderCompletionSystemPrompt(once, config, tools)

	if twice != once {
		t.Fatalf("guidance emitted twice:\nfirst: %q\nsecond: %q", once, twice)
	}
}

// TestCoderCompletionSystemPromptEmptyPrompt covers a coder config with no
// configured system prompt: the guidance alone must be well formed.
func TestCoderCompletionSystemPromptEmptyPrompt(t *testing.T) {
	config := map[string]interface{}{"tools": []interface{}{"coder_bash", nativecoder.AllTasksCompletedToolName}}
	tools := []string{"coder_bash", nativecoder.AllTasksCompletedToolName}

	got := coderCompletionSystemPrompt("   ", config, tools)

	if got != nativecoder.CoderTaskCompletionGuidance() {
		t.Fatalf("got %q, want the guidance alone", got)
	}
}
