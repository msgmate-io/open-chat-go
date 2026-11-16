package msgmate

import (
	"encoding/json"
	"testing"

	wsapi "backend/api/websocket"
)

func TestToolCallPhase(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		firstSeen bool
		want      wsapi.ToolCallPhase
	}{
		{name: "first sight starts", status: ToolCallStatusOngoing, firstSeen: true, want: wsapi.ToolCallPhaseStart},
		{name: "ongoing updates", status: ToolCallStatusOngoing, firstSeen: false, want: wsapi.ToolCallPhaseUpdate},
		{name: "pending confirmation updates", status: ToolCallStatusPendingConfirmation, firstSeen: false, want: wsapi.ToolCallPhaseUpdate},
		{name: "succeeded ends", status: ToolCallStatusSucceeded, firstSeen: false, want: wsapi.ToolCallPhaseEnd},
		{name: "failed ends", status: ToolCallStatusFailed, firstSeen: false, want: wsapi.ToolCallPhaseEnd},
		{name: "first sight wins over terminal", status: ToolCallStatusSucceeded, firstSeen: true, want: wsapi.ToolCallPhaseStart},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolCallPhase(tt.status, tt.firstSeen); got != tt.want {
				t.Fatalf("toolCallPhase(%q, %v) = %q, want %q", tt.status, tt.firstSeen, got, tt.want)
			}
		})
	}
}

func TestIsTerminalToolStatus(t *testing.T) {
	for _, status := range []string{ToolCallStatusSucceeded, ToolCallStatusFailed, " SUCCEEDED "} {
		if !isTerminalToolStatus(status) {
			t.Fatalf("isTerminalToolStatus(%q) = false, want true", status)
		}
	}
	for _, status := range []string{ToolCallStatusOngoing, ToolCallStatusPendingConfirmation, ""} {
		if isTerminalToolStatus(status) {
			t.Fatalf("isTerminalToolStatus(%q) = true, want false", status)
		}
	}
}

// TestFineStreamEmitterNilSafe pins that a turn without a websocket handler
// (tests, CLI paths) never panics and never emits.
func TestFineStreamEmitterNilSafe(t *testing.T) {
	emitter := newFineStreamEmitter(nil, "chat", "sender", "sess", nil)
	if emitter != nil {
		t.Fatalf("newFineStreamEmitter(nil) = %v, want nil", emitter)
	}
	emitter.status("thinking")
	emitter.reasoning("chunk")
	emitter.toolCall("call-1", "bash", ToolCallStatusOngoing, true, nil)
	emitter.snapshot("text", []string{"r"}, nil)
}

// TestFineStreamEmitterStatusDedupes pins that a phase transition is emitted
// once, not once per chunk.
func TestFineStreamEmitterStatusDedupes(t *testing.T) {
	emitter := &fineStreamEmitter{chatUUID: "chat", senderUUID: "sender", sessionID: "sess"}
	emitter.status("thinking")
	if emitter.lastPhase != "thinking" {
		t.Fatalf("lastPhase = %q, want thinking", emitter.lastPhase)
	}
	emitter.status("thinking")
	if emitter.lastPhase != "thinking" {
		t.Fatalf("lastPhase = %q, want thinking", emitter.lastPhase)
	}
	emitter.status("responding")
	if emitter.lastPhase != "responding" {
		t.Fatalf("lastPhase = %q, want responding", emitter.lastPhase)
	}
	// An empty phase never changes the dedupe state.
	emitter.status("")
	if emitter.lastPhase != "responding" {
		t.Fatalf("lastPhase = %q, want responding after empty phase", emitter.lastPhase)
	}
}

// TestFineStreamEmitterHandlerWithoutMessagesIsNilSafe pins that a handler that
// was not initialised with a message renderer (or an emitter built outside the
// constructor) never dereferences a nil field on any emit path.
func TestFineStreamEmitterHandlerWithoutMessagesIsNilSafe(t *testing.T) {
	raw := &fineStreamEmitter{
		handler:    &wsapi.WebSocketHandler{},
		chatUUID:   "chat",
		senderUUID: "sender",
		sessionID:  "sess",
	}
	raw.status("thinking")
	raw.reasoning("chunk")
	raw.toolCall("call-1", "bash", ToolCallStatusOngoing, true, map[string]interface{}{"k": "v"})
	raw.snapshot("text", []string{"r"}, []interface{}{"call-1"})
	if raw.lastPhase != "thinking" {
		t.Fatalf("lastPhase = %q, want thinking", raw.lastPhase)
	}
}

// TestFineStreamEmitterEmitsOneFramePerPhaseTransition pins the wiring end to
// end: the emitter renders exactly one status frame per transition (not one per
// chunk) and stamps the routing keys on it.
func TestFineStreamEmitterEmitsOneFramePerPhaseTransition(t *testing.T) {
	var frames [][]byte
	emitter := &fineStreamEmitter{
		handler:    &wsapi.WebSocketHandler{MessageHandler: &wsapi.Messages{}},
		chatUUID:   "chat",
		senderUUID: "sender",
		sessionID:  "sess",
		seq:        func() int64 { return 7 },
		sink:       func(frame []byte) { frames = append(frames, frame) },
	}

	emitter.status("thinking")
	emitter.status("thinking")
	emitter.status("responding")
	emitter.status("responding")
	emitter.status("tool_call")

	if len(frames) != 3 {
		t.Fatalf("emitter published %d status frames, want 3", len(frames))
	}
	for i, want := range []string{"thinking", "responding", "tool_call"} {
		var event wsapi.StreamEvent
		if err := json.Unmarshal(frames[i], &event); err != nil {
			t.Fatalf("frame %d is not a stream event: %v", i, err)
		}
		if event.Type != wsapi.TypeStatusChanged {
			t.Fatalf("frame %d type = %q, want %q", i, event.Type, wsapi.TypeStatusChanged)
		}
		if event.Content.Status != want {
			t.Fatalf("frame %d status = %q, want %q", i, event.Content.Status, want)
		}
		if event.Content.ChatUUID != "chat" || event.Content.SenderUUID != "sender" {
			t.Fatalf("frame %d lost routing keys: %+v", i, event.Content)
		}
		if event.Content.Seq != 7 {
			t.Fatalf("frame %d seq = %d, want 7", i, event.Content.Seq)
		}
	}
}
