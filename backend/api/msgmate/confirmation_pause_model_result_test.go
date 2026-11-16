package msgmate

import (
	"encoding/json"
	"strings"
	"testing"
)

// ci#1116: a paused confirmable call must hand the model explicit
// pending-confirmation guidance instead of the bare confirm-action payload,
// otherwise the agent re-issues the call, which pauses again and queues
// another continuation ("Waiting for your confirmation ..." churn).
func TestBuildConfirmationPauseModelResult(t *testing.T) {
	t.Run("shared guidance names the tool and forbids retry", func(t *testing.T) {
		got := buildConfirmationPauseModelResult("coder_bash", "")
		for _, want := range []string{"coder_bash", "NOT executed", "Do not retry"} {
			if !strings.Contains(got, want) {
				t.Fatalf("guidance %q does not contain %q", got, want)
			}
		}
	})

	t.Run("empty tool name still yields usable guidance", func(t *testing.T) {
		got := buildConfirmationPauseModelResult("   ", "")
		if strings.Contains(got, "``") {
			t.Fatalf("empty tool name produced an empty code span: %q", got)
		}
		if !strings.Contains(got, "the requested tool") {
			t.Fatalf("expected placeholder tool name, got %q", got)
		}
	})

	t.Run("tool specific block message wins", func(t *testing.T) {
		got := buildConfirmationPauseModelResult("coder_ask", "  ask the user directly  ")
		if got != "ask the user directly" {
			t.Fatalf("block message not preferred: %q", got)
		}
	})

	t.Run("deterministic so the prompt prefix stays cacheable", func(t *testing.T) {
		first := buildConfirmationPauseModelResult("coder_bash", "")
		for i := 0; i < 5; i++ {
			if again := buildConfirmationPauseModelResult("coder_bash", ""); again != first {
				t.Fatalf("guidance is not deterministic:\n%q\n%q", first, again)
			}
		}
	})
}

func TestIsConfirmActionPayload(t *testing.T) {
	valid := buildConfirmationSuggestion("coder_bash", map[string]interface{}{"command": "ls"}, false)

	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"confirm-action payload", valid, true},
		{"empty", "", false},
		{"blank", "   \n\t", false},
		{"plain text", "Waiting for your confirmation to run coder_bash.", false},
		{"other type", `{"type":"tool-result"}`, false},
		{"type not a string", `{"type":42}`, false},
		{"malformed json", `{"type":"confirm-action"`, false},
		{"json array", `[{"type":"confirm-action"}]`, false},
		{"object without type", `{"status":"pending_confirmation"}`, false},
	}
	for _, tc := range cases {
		if got := isConfirmActionPayload(tc.raw); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// The stored result of a paused call keeps the confirm-action payload until the
// user decides, so the replay path has to map it back to the same guidance the
// model saw when the turn paused.
func TestConfirmationResultForModel(t *testing.T) {
	payload := buildConfirmationSuggestion("coder_bash", map[string]interface{}{"command": "ls"}, true)

	got := confirmationResultForModel("coder_bash", payload)
	if got == payload {
		t.Fatalf("confirm-action payload was replayed verbatim into the model context")
	}
	if isConfirmActionPayload(got) {
		t.Fatalf("replayed result is still machine readable payload: %q", got)
	}
	if got != buildConfirmationPauseModelResult("coder_bash", "") {
		t.Fatalf("replay does not reuse the pause guidance: %q", got)
	}
	if !strings.Contains(got, "coder_bash") {
		t.Fatalf("replayed guidance lost the tool name: %q", got)
	}

	t.Run("non payload results are untouched", func(t *testing.T) {
		for _, stored := range []string{
			"total 4\ndrwxr-xr-x 1 root root",
			`{"type":"tool-result","ok":true}`,
			"",
		} {
			if got := confirmationResultForModel("coder_bash", stored); got != stored {
				t.Fatalf("result %q was rewritten to %q", stored, got)
			}
		}
	})

	t.Run("a decided call keeps its executed result", func(t *testing.T) {
		executed := `{"ok":true,"stdout":"file.txt"}`
		if got := confirmationResultForModel("coder_bash", executed); got != executed {
			t.Fatalf("executed result was rewritten to %q", got)
		}
	})
}

// The payload stays what the UI/persistence layer consumes: replacing the
// model-facing text must not change the stored shape.
func TestConfirmationSuggestionPayloadShapeIsStable(t *testing.T) {
	raw := buildConfirmationSuggestion("coder_bash", map[string]interface{}{"command": "ls"}, true)
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("confirm-action payload is not valid json: %v", err)
	}
	if payload["type"] != "confirm-action" {
		t.Fatalf("unexpected payload type: %v", payload["type"])
	}
	if payload["status"] != "pending_confirmation" {
		t.Fatalf("unexpected payload status: %v", payload["status"])
	}
	if payload["tool_name"] != "coder_bash" {
		t.Fatalf("unexpected payload tool name: %v", payload["tool_name"])
	}
	if payload["continue_after_execute"] != true {
		t.Fatalf("continue_after_execute flag lost: %v", payload["continue_after_execute"])
	}
}
