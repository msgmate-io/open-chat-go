package msgmate

import (
	"encoding/json"
	"testing"
)

// TestCollectCoderAskQuestions reflects ci#947: the coder sandbox regression on
// main reported that a coder_ask question resolved in an autonomous session
// reached the verifier without its expected options. The resolver emits an
// `ask-question` payload as the tool result; the core must surface it on the
// tool-call row (and in the message-level ask_questions aggregate) with the
// options intact instead of collapsing it into plain text.
func TestCollectCoderAskQuestions(t *testing.T) {
	t.Run("preserves options and question", func(t *testing.T) {
		raw := `{"type":"ask-question","question":"Which runtime should I use?","options":["native","opencode"],"id":"call_1"}`
		meta, ok := parseCoderAskPayload(raw)
		if !ok {
			t.Fatalf("parseCoderAskPayload returned ok=false for %s", raw)
		}
		options, ok := meta["options"].([]interface{})
		if !ok {
			t.Fatalf("payload options missing or wrong type: %#v", meta["options"])
		}
		if len(options) != 2 {
			t.Fatalf("expected 2 options, got %d: %#v", len(options), meta["options"])
		}
	})

	t.Run("rejects payloads without options", func(t *testing.T) {
		for _, raw := range []string{
			``,
			`not json`,
			`{"type":"confirm-action","options":["x"]}`,
			`{"type":"ask-question","question":"no options here"}`,
			`{"type":"ask-question","question":"empty options","options":[]}`,
		} {
			if meta, ok := parseCoderAskPayload(raw); ok {
				t.Fatalf("parseCoderAskPayload(%q) = %#v, true; want false", raw, meta)
			}
		}
	})

	t.Run("collects payload with stable id", func(t *testing.T) {
		raw := `{"type":"ask-question","question":"Q?","options":["a","b"]}`
		toolCalls := []interface{}{
			map[string]interface{}{
				"id":          "call_ask_1",
				"name":        "coder_ask",
				"status":      "succeeded",
				"ask_question": map[string]interface{}{},
			},
		}
		toolCall, _ := toolCalls[0].(map[string]interface{})
		askMeta, ok := parseCoderAskPayload(raw)
		if !ok {
			t.Fatal("parseCoderAskPayload returned ok=false")
		}
		toolCall["ask_question"] = askMeta
		questions := collectCoderAskQuestions(toolCalls)
		if len(questions) != 1 {
			t.Fatalf("expected 1 question, got %d", len(questions))
		}
		question, _ := questions[0].(map[string]interface{})
		if id, _ := question["id"].(string); id != "call_ask_1" {
			t.Fatalf("expected id call_ask_1, got %q", id)
		}
	})

	t.Run("ignores unrelated tool calls", func(t *testing.T) {
		toolCalls := []interface{}{
			map[string]interface{}{"id": "1", "name": "coder_read", "status": "succeeded"},
			map[string]interface{}{"id": "2", "name": "coder_ask", "status": "succeeded"},
		}
		if questions := collectCoderAskQuestions(toolCalls); len(questions) != 0 {
			t.Fatalf("expected no questions, got %#v", questions)
		}
	})
}

// TestAskQuestionRoundTripJSON verifies the parsed ask-question payload is
// JSON-round-trippable, ie the options survive being re-marshaled onto the
// message meta.
func TestAskQuestionRoundTripJSON(t *testing.T) {
	raw := `{"type":"ask-question","question":"pick one","options":["red","blue"]}`
	meta, ok := parseCoderAskPayload(raw)
	if !ok {
		t.Fatal("parseCoderAskPayload returned ok=false")
	}
	reencoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(reencoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	options, ok := decoded["options"].([]interface{})
	if !ok {
		t.Fatalf("decoded options missing: %#v", decoded)
	}
	if len(options) != 2 {
		t.Fatalf("expected 2 options after round-trip, got %d", len(options))
	}
}
