package websocket

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestParseStreamMode(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  StreamMode
	}{
		{name: "absent defaults to legacy", query: "", want: StreamModeLegacy},
		{name: "unrelated params", query: "?foo=bar", want: StreamModeLegacy},
		{name: "unknown value", query: "?stream=coarse", want: StreamModeLegacy},
		{name: "fine", query: "?stream=fine", want: StreamModeFine},
		{name: "fine case insensitive", query: "?stream=FINE", want: StreamModeFine},
		{name: "fine in list", query: "?stream=legacy,fine", want: StreamModeFine},
		{name: "fine with spaces", query: "?stream=%20fine%20", want: StreamModeFine},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := url.ParseQuery(strings.TrimPrefix(tt.query, "?"))
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", tt.query, err)
			}
			if got := ParseStreamMode(values); got != tt.want {
				t.Fatalf("ParseStreamMode(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestFineStreamEventTypesAreKnown(t *testing.T) {
	for _, eventType := range FineStreamEventTypes {
		if !IsFineStreamEventType(eventType) {
			t.Fatalf("IsFineStreamEventType(%q) = false, want true", eventType)
		}
	}
	// Legacy frame types must never be classified as fine frames.
	for _, legacy := range []string{"new_partial_message", "new_message", "start_partial_message", "end_partial_message"} {
		if IsFineStreamEventType(legacy) {
			t.Fatalf("IsFineStreamEventType(%q) = true, want false", legacy)
		}
	}
}

func TestToolCallEventPhases(t *testing.T) {
	m := &Messages{}
	content := StreamEventContent{ChatUUID: "chat", SenderUUID: "sender", ToolCallID: "call-1"}

	tests := []struct {
		phase    ToolCallPhase
		wantType string
	}{
		{ToolCallPhaseStart, TypeToolCallStarted},
		{ToolCallPhaseUpdate, TypeToolCallUpdated},
		{ToolCallPhaseEnd, TypeToolCallEnded},
	}
	for _, tt := range tests {
		frame := m.ToolCallEvent(tt.phase, content)
		var decoded StreamEvent
		if err := json.Unmarshal(frame, &decoded); err != nil {
			t.Fatalf("unmarshal %s frame: %v", tt.phase, err)
		}
		if decoded.Type != tt.wantType {
			t.Fatalf("phase %s produced type %q, want %q", tt.phase, decoded.Type, tt.wantType)
		}
		if decoded.Content.Phase != string(tt.phase) {
			t.Fatalf("phase %s produced content.phase %q", tt.phase, decoded.Content.Phase)
		}
		if decoded.Content.ToolCallID != "call-1" {
			t.Fatalf("tool_call_id lost: %q", decoded.Content.ToolCallID)
		}
	}
}

func TestFineFrameEnvelopeShape(t *testing.T) {
	m := &Messages{}
	content := StreamEventContent{ChatUUID: "chat", SenderUUID: "sender", SessionID: "sess", Seq: 7}

	frames := map[string][]byte{
		TypeStatusChanged:   m.StatusEvent(content),
		TypeReasoningDelta:  m.ReasoningDeltaEvent(content),
		TypePartialSnapshot: m.PartialSnapshotEvent(content),
	}
	for wantType, frame := range frames {
		var decoded StreamEvent
		if err := json.Unmarshal(frame, &decoded); err != nil {
			t.Fatalf("unmarshal %s frame: %v", wantType, err)
		}
		if decoded.Type != wantType {
			t.Fatalf("frame type = %q, want %q", decoded.Type, wantType)
		}
		if decoded.Content.ChatUUID != "chat" || decoded.Content.SenderUUID != "sender" {
			t.Fatalf("%s frame lost routing keys: %+v", wantType, decoded.Content)
		}
		if decoded.Content.Seq != 7 {
			t.Fatalf("%s frame lost seq: %d", wantType, decoded.Content.Seq)
		}
	}
}

// newTestSubscriber builds a subscriber with a buffered channel so the publish
// helpers can be exercised without a real websocket connection.
func newTestSubscriber(mode StreamMode, userUUID, chatUUID, contentMode string) *Subscriber {
	return &Subscriber{
		UserUUID:    userUUID,
		ChatUUID:    chatUUID,
		ContentMode: contentMode,
		StreamMode:  mode,
		msgs:        make(chan []byte, 8),
		closeSlow:   func() {},
	}
}

func drain(s *Subscriber) [][]byte {
	var out [][]byte
	for {
		select {
		case msg := <-s.msgs:
			out = append(out, msg)
		default:
			return out
		}
	}
}

func TestPublishFineInChannelSkipsLegacySubscribers(t *testing.T) {
	handler := NewWebSocketHandler()
	legacy := newTestSubscriber(StreamModeLegacy, "user-1", "", "")
	fine := newTestSubscriber(StreamModeFine, "user-1", "", "")
	otherUser := newTestSubscriber(StreamModeFine, "user-2", "", "")
	handler.addSubscriber(legacy)
	handler.addSubscriber(fine)
	handler.addSubscriber(otherUser)

	frame := (&Messages{}).StatusEvent(StreamEventContent{ChatUUID: "chat", SenderUUID: "user-1", Status: "thinking"})
	handler.PublishFineInChannel(frame, "user-1")

	if got := drain(legacy); len(got) != 0 {
		t.Fatalf("legacy subscriber received %d fine frames, want 0", len(got))
	}
	if got := drain(otherUser); len(got) != 0 {
		t.Fatalf("other user received %d fine frames, want 0", len(got))
	}
	got := drain(fine)
	if len(got) != 1 {
		t.Fatalf("fine subscriber received %d frames, want 1", len(got))
	}
	if string(got[0]) != string(frame) {
		t.Fatalf("fine subscriber frame mismatch: %s", got[0])
	}
}

func TestPublishFineInChatChannelSkipsLegacyAndCensored(t *testing.T) {
	handler := NewWebSocketHandler()
	legacy := newTestSubscriber(StreamModeLegacy, "", "chat-1", "unsecure")
	fine := newTestSubscriber(StreamModeFine, "", "chat-1", "unsecure")
	censored := newTestSubscriber(StreamModeFine, "", "chat-1", "censored")
	otherChat := newTestSubscriber(StreamModeFine, "", "chat-2", "unsecure")
	handler.addSubscriber(legacy)
	handler.addSubscriber(fine)
	handler.addSubscriber(censored)
	handler.addSubscriber(otherChat)

	frame := (&Messages{}).ToolCallEvent(ToolCallPhaseStart, StreamEventContent{
		ChatUUID:   "chat-1",
		SenderUUID: "user-1",
		ToolCallID: "call-1",
	})
	handler.PublishFineInChatChannel(frame, "chat-1")

	if got := drain(legacy); len(got) != 0 {
		t.Fatalf("legacy subscriber received %d fine frames, want 0", len(got))
	}
	if got := drain(censored); len(got) != 0 {
		t.Fatalf("censored share received %d fine frames, want 0", len(got))
	}
	if got := drain(otherChat); len(got) != 0 {
		t.Fatalf("other chat received %d fine frames, want 0", len(got))
	}
	if got := drain(fine); len(got) != 1 {
		t.Fatalf("fine subscriber received %d frames, want 1", len(got))
	}
}

// TestLegacyPublishUnaffectedByStreamMode pins the backward compatibility
// contract: the historic publish helpers deliver to every subscriber of the
// user/chat regardless of the negotiated stream mode.
func TestLegacyPublishUnaffectedByStreamMode(t *testing.T) {
	handler := NewWebSocketHandler()
	legacy := newTestSubscriber(StreamModeLegacy, "user-1", "", "")
	fine := newTestSubscriber(StreamModeFine, "user-1", "", "")
	handler.addSubscriber(legacy)
	handler.addSubscriber(fine)

	frame := (&Messages{}).NewPartialMessage("chat", "user-1", "sess", 1, "chunk", nil, nil, nil, nil)
	handler.PublishInChannel(frame, "user-1")

	if got := drain(legacy); len(got) != 1 {
		t.Fatalf("legacy subscriber received %d legacy frames, want 1", len(got))
	}
	if got := drain(fine); len(got) != 1 {
		t.Fatalf("fine subscriber received %d legacy frames, want 1", len(got))
	}
}
