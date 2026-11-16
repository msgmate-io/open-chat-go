package tui

import (
	"encoding/json"
	"testing"
)

func TestStreamURLNegotiatesFineStreamAndKeepsAuth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "adds stream=fine and keeps the token",
			in:   "wss://api.msgmate.io/api/v1/interaction/messages/ws?token=abc",
			want: "wss://api.msgmate.io/api/v1/interaction/messages/ws?stream=fine&token=abc",
		},
		{
			name: "idempotent",
			in:   "wss://api.msgmate.io/api/v1/interaction/messages/ws?stream=fine&token=abc",
			want: "wss://api.msgmate.io/api/v1/interaction/messages/ws?stream=fine&token=abc",
		},
		{
			name: "overrides a coarse mode",
			in:   "wss://api.msgmate.io/ws?stream=coarse",
			want: "wss://api.msgmate.io/ws?stream=fine",
		},
		{
			name: "unparsable url is left untouched",
			in:   "://nonsense",
			want: "://nonsense",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StreamURL(tc.in); got != tc.want {
				t.Fatalf("StreamURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func mustApply(t *testing.T, s *StreamState, frames ...string) StreamUpdate {
	t.Helper()
	var last StreamUpdate
	for _, frame := range frames {
		update, err := s.Apply([]byte(frame))
		if err != nil {
			t.Fatalf("Apply(%s): %v", frame, err)
		}
		last = update
	}
	return last
}

func TestApplyRendersToolCallsIncrementally(t *testing.T) {
	state := &StreamState{}
	update := mustApply(t, state,
		`{"type":"tool_call_started","content":{"chat_uuid":"chat-1","tool_call_id":"call_1","status":"running","payload":{"name":"coder_read","summary":{"path":"main.go"}}}}`)
	if update.ToolCallIndex != 0 || update.None() {
		t.Fatalf("started frame should touch row 0, got %+v", update)
	}
	if len(state.ToolCalls) != 1 || state.ToolCalls[0].Name != "coder_read" || state.ToolCalls[0].Done {
		t.Fatalf("unexpected tool call after started: %+v", state.ToolCalls)
	}

	update = mustApply(t, state,
		`{"type":"tool_call_updated","content":{"chat_uuid":"chat-1","tool_call_id":"call_1","status":"writing"}}`)
	if update.ToolCallIndex != 0 || state.ToolCalls[0].Status != "writing" {
		t.Fatalf("updated frame should mutate row 0, got %+v / %+v", update, state.ToolCalls)
	}

	update = mustApply(t, state,
		`{"type":"tool_call_ended","content":{"chat_uuid":"chat-1","tool_call_id":"call_1","payload":{"summary":"done reading"}}}`)
	if update.ToolCallIndex != 0 {
		t.Fatalf("ended frame should touch row 0, got %+v", update)
	}
	if !state.ToolCalls[0].Done || state.ToolCalls[0].Status != "writing" {
		t.Fatalf("ended frame should only mark the row done: %+v", state.ToolCalls[0])
	}

	update = mustApply(t, state,
		`{"type":"tool_call_started","content":{"chat_uuid":"chat-1","tool_call_id":"call_2","payload":{"name":"coder_bash"}}}`)
	if update.ToolCallIndex != 1 || len(state.ToolCalls) != 2 {
		t.Fatalf("second tool call should append at index 1, got %+v / %d", update, len(state.ToolCalls))
	}
	// Rows keep their index across later frames so the renderer repaints one
	// line instead of rebuilding the transcript.
	update = mustApply(t, state,
		`{"type":"tool_call_updated","content":{"chat_uuid":"chat-1","tool_call_id":"call_1","status":"done"}}`)
	if update.ToolCallIndex != 0 {
		t.Fatalf("late update of the first call should still be row 0, got %d", update.ToolCallIndex)
	}
}

func TestApplyReasoningDeltaAccumulatesAndSnapshotReconciles(t *testing.T) {
	state := &StreamState{}
	if update := mustApply(t, state,
		`{"type":"reasoning_delta","content":{"chat_uuid":"chat-1","delta":"think "}}`); !update.ReasoningChanged {
		t.Fatalf("reasoning delta should flag the reasoning row: %+v", update)
	}
	mustApply(t, state, `{"type":"reasoning_delta","content":{"chat_uuid":"chat-1","delta":"harder"}}`)
	if state.Reasoning != "think harder" {
		t.Fatalf("reasoning should accumulate deltas, got %q", state.Reasoning)
	}

	update := mustApply(t, state,
		`{"type":"partial_snapshot","content":{"chat_uuid":"chat-1","payload":{"text":"final answer","reasoning":"think harder"}}}`)
	if !update.Snapshot || !update.AssistantChanged {
		t.Fatalf("snapshot should replace the assistant text: %+v", update)
	}
	if state.Assistant != "final answer" || state.Reasoning != "think harder" {
		t.Fatalf("unexpected state after snapshot: %q / %q", state.Assistant, state.Reasoning)
	}
	// A snapshot identical to the visible state must not repaint anything.
	if update := mustApply(t, state,
		`{"type":"partial_snapshot","content":{"chat_uuid":"chat-1","payload":{"text":"final answer","reasoning":"think harder"}}}`); !update.None() {
		t.Fatalf("redundant snapshot should be a no-op, got %+v", update)
	}
}

func TestApplyStatusAndFrameFiltering(t *testing.T) {
	state := &StreamState{}
	update := mustApply(t, state, `{"type":"status_changed","content":{"chat_uuid":"chat-1","status":"calling tools"}}`)
	if !update.StatusChanged || state.Status != "calling tools" {
		t.Fatalf("status frame should update the footer: %+v / %q", update, state.Status)
	}
	if update := mustApply(t, state, `{"type":"status_changed","content":{"chat_uuid":"chat-1","status":"calling tools"}}`); !update.None() {
		t.Fatalf("repeated status should be a no-op, got %+v", update)
	}

	// Frames of another chat and coarse legacy frames are ignored, so a
	// socket that still delivers the old array payload cannot corrupt the
	// incremental state.
	if update := mustApply(t, state,
		`{"type":"tool_call_started","content":{"chat_uuid":"chat-2","tool_call_id":"x","payload":{"name":"nope"}}}`); !update.None() {
		t.Fatalf("foreign chat frame should be ignored, got %+v", update)
	}
	if len(state.ToolCalls) != 0 {
		t.Fatalf("foreign chat frame must not add rows: %+v", state.ToolCalls)
	}
	if update := mustApply(t, state, `{"type":"message_update","content":{"chat_uuid":"chat-1"}}`); !update.None() {
		t.Fatalf("coarse frame should be ignored, got %+v", update)
	}
}

func TestApplyToolCallPayloadCarriesRawJSON(t *testing.T) {
	state := &StreamState{}
	if _, err := state.Apply([]byte(`{"type":"tool_call_started","content":{"tool_call_id":"call_9","payload":{"name":"coder_bash","input":{"commands":["go test ./tui/..."],"reason":"verify"}}}}`)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(state.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", state.ToolCalls)
	}
	encoded, err := json.Marshal(state.ToolCalls[0].Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if want := `{"input":{"commands":["go test ./tui/..."],"reason":"verify"},"name":"coder_bash"}`; string(encoded) != want {
		t.Fatalf("payload round trip drifted:\n got %s\nwant %s", encoded, want)
	}
}
