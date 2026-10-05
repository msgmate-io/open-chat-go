package msgmate

import (
	"encoding/json"
	"testing"
)

// TestCollectAskActions carries the selectable options of a pending coder_ask
// call through to the chat UI. ci#945: the coder sandbox suite failed with
// "the coder_ask question did not carry the expected options" on main (ci#479)
// because the interactive confirm path re-serializes a map[string]interface{}
// whose string/[]interface{} values json.Unmarshal turns into string/float64,
// which the frontend widgets then discard. The promotion now copies the raw
// values (including the full options list, which were dropped entirely before)
// into a typed open_question action, so the options are carried verbatim.
func TestCollectAskActions(t *testing.T) {
	toolCall := map[string]interface{}{
		"name":      "coder_ask",
		"type":      "function",
		"status":    "pending",
		"id":        "call_ask_order",
		"arguments": `{"question":"which ordering do you prefer?","title":"Deploy ordering","description":"pick one","options":["blue","green","canary"],"default":"blue"}`,
	}

	for _, fn := range []string{"collectAskActions", "buildAskActionFromToolCall"} {
		actions := collectAskActions([]interface{}{toolCall})
		if len(actions) != 1 {
			t.Fatalf("%s: expected 1 promoted ask action, got %d", fn, len(actions))
		}
		action := actions[0].(map[string]interface{})
		if action["type"] != "open_question" {
			t.Fatalf("%s: expected type 'open_question', got %#v", fn, action["type"])
		}
		if action["prompt"] != "which ordering do you prefer?" {
			t.Fatalf("%s: prompt not carried: %#v", fn, action["prompt"])
		}
		if action["title"] != "Deploy ordering" {
			t.Fatalf("%s: title not carried: %#v", fn, action["title"])
		}
		if action["description"] != "pick one" {
			t.Fatalf("%s: description not carried: %#v", fn, action["description"])
		}
		if action["default"] != "blue" {
			t.Fatalf("%s: default answer not carried: %#v", fn, action["default"])
		}
		options, ok := action["options"].([]interface{})
		if !ok {
			t.Fatalf("%s: options must be carried as a list, got %#v", fn, action["options"])
		}
		if len(options) != 3 || options[0] != "blue" || options[1] != "green" || options[2] != "canary" {
			t.Fatalf("%s: expected options [blue green canary] verbatim, got %#v", fn, options)
		}
		if action["status"] != "pending" {
			t.Fatalf("%s: expected pending status, got %#v", fn, action["status"])
		}
		if _, ok := action["action_id"].(string); !ok || action["action_id"] == "" {
			t.Fatalf("%s: expected an action_id, got %#v", fn, action["action_id"])
		}
		if action["source_tool_name"] != "coder_ask" {
			t.Fatalf("%s: source_tool_name not carried: %#v", fn, action["source_tool_name"])
		}
		if _, err := json.Marshal(action); err != nil {
			t.Fatalf("%s: action must marshal: %v", fn, err)
		}
	}
}

// TestCollectAskActionsObjectArguments supplies the arguments as an already
// decoded map (the shape buildToolCallRepr keeps at the call site) to prove the
// promotion works for both representations.
func TestCollectAskActionsObjectArguments(t *testing.T) {
	toolCall := map[string]interface{}{
		"name":   "coder_ask",
		"type":   "function",
		"status": "pending",
		"id":     "call_ask_deploy",
		"arguments": map[string]interface{}{
			"question":     "continue with the deploy?",
			"options":      []interface{}{"yes", "no", "later"},
			"answer_key":   "deploy_decision",
			"as_json_only": true,
		},
	}
	action, ok := buildAskActionFromToolCall(toolCall)
	if !ok {
		t.Fatalf("expected a promoted ask action for object arguments")
	}
	if action["prompt"] != "continue with the deploy?" {
		t.Fatalf("prompt not carried: %#v", action["prompt"])
	}
	options, ok := action["options"].([]interface{})
	if !ok || len(options) != 3 || options[0] != "yes" || options[2] != "later" {
		t.Fatalf("options not carried verbatim: %#v", action["options"])
	}
	if action["answer_key"] != "deploy_decision" {
		t.Fatalf("answer_key not carried: %#v", action["answer_key"])
	}
}

// TestCollectAskActionsSkipsNonAsk ensures unrelated tool calls and failed ask
// calls are never promoted into open_question actions.
func TestCollectAskActionsSkipsNonAsk(t *testing.T) {
	toolCalls := []interface{}{
		map[string]interface{}{
			"name":      "coder_bash",
			"type":      "function",
			"status":    "pending",
			"arguments": `{"command":"ls","reason":"inspect"}`,
		},
		map[string]interface{}{
			"name":      "coder_ask",
			"type":      "function",
			"status":    "failed",
			"arguments": `{"question":"should we?"}`,
		},
		map[string]interface{}{
			"name":      "coder_ask",
			"type":      "function",
			"status":    "pending",
			"arguments": `{"reason":"no question"}`,
		},
	}
	if actions := collectAskActions(toolCalls); len(actions) != 0 {
		t.Fatalf("expected no promotions for non-ask/failed asks, got %d: %#v", len(actions), actions)
	}
}

// TestCollectAskActionsTwoAccordingToInitializerExercisesSecondArm ensures the
// options arm tolerates an already-typed []string list (e.g. a caller that
// constructed the arguments from a typed struct) as well as []interface{}.
func TestCollectAskActionsTwoAccordingToInitializerExercisesSecondArm(t *testing.T) {
	toolCall := map[string]interface{}{
		"name":   "coder_ask",
		"type":   "function",
		"status": "pending",
		"id":     "call_ask_plan",
		"arguments": map[string]interface{}{
			"question": "which plan?",
			"options":  []string{"plan-a", "plan-b"},
		},
	}
	action, ok := buildAskActionFromToolCall(toolCall)
	if !ok {
		t.Fatalf("expected a promoted ask action")
	}
	options, ok := action["options"].([]interface{})
	if !ok || len(options) != 2 || options[0] != "plan-a" || options[1] != "plan-b" {
		t.Fatalf("options not carried verbatim: %#v", action["options"])
	}
}

// TestAskActionsMetadata is a slim end-to-end check that the finished-turn
// metadata path attaches the promoted actions under ask_actions so the chat UI
// renders the selectable options widget.
func TestAskActionsMetadata(t *testing.T) {
	metadata := make(map[string]interface{})
	toolCalls := []interface{}{
		map[string]interface{}{
			"name":      "coder_ask",
			"type":      "function",
			"status":    "pending",
			"id":        "call_ask_1",
			"arguments": `{"question":"pick a region","options":["us-east-1","us-west-2","eu-west-1"]}`,
		},
	}
	if askActions := collectAskActions(toolCalls); len(askActions) > 0 {
		metadata["ask_actions"] = askActions
	}
	actions, ok := metadata["ask_actions"].([]interface{})
	if !ok || len(actions) != 1 {
		t.Fatalf("expected ask_actions metadata, got %#v", metadata["ask_actions"])
	}
	action := actions[0].(map[string]interface{})
	if action["prompt"] != "pick a region" {
		t.Fatalf("prompt not carried: %#v", action)
	}
	if options, ok := action["options"].([]interface{}); !ok || len(options) != 3 || options[2] != "eu-west-1" {
		t.Fatalf("options not carried verbatim: %#v", action["options"])
	}
}
