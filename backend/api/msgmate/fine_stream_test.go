package msgmate

import (
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
}
