package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// End-to-end guard for the client half of ci#1104: a real websocket round trip
// through Run must (a) negotiate `?stream=fine` on the already authenticated
// interaction URL, (b) deliver one incremental update per visible frame instead
// of a full-array redraw and (c) survive frames it does not understand.

func TestFineStreamClientNegotiatesAndRendersIncrementally(t *testing.T) {
	frames := []string{
		`{"type":"status_changed","content":{"chat_uuid":"chat-1","status":"calling tools"}}`,
		`{"type":"tool_call_started","content":{"chat_uuid":"chat-1","tool_call_id":"call_1","status":"running","payload":{"name":"coder_read"}}}`,
		// A frame type this client may not know yet must not kill the view.
		`{"type":"some_future_event","content":{"chat_uuid":"chat-1"}}`,
		// A foreign chat frame is dropped, it must not repaint anything.
		`{"type":"tool_call_started","content":{"chat_uuid":"chat-2","tool_call_id":"call_x","payload":{"name":"nope"}}}`,
		`{"type":"tool_call_updated","content":{"chat_uuid":"chat-1","tool_call_id":"call_1","status":"writing"}}`,
		`{"type":"reasoning_delta","content":{"chat_uuid":"chat-1","delta":"thinking"}}`,
		`{"type":"tool_call_ended","content":{"chat_uuid":"chat-1","tool_call_id":"call_1"}}`,
		`{"type":"partial_snapshot","content":{"chat_uuid":"chat-1","payload":{"text":"final","reasoning":"thinking"}}}`,
	}

	var gotMode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMode = r.URL.Query().Get(QueryParamStream)
		if got := r.URL.Query().Get("token"); got != "abc" {
			t.Errorf("auth token must survive the negotiation, got %q", got)
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for _, frame := range frames {
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				return
			}
		}
		// Close so Run returns on its own; the client treats it as EOF.
		conn.Close(websocket.StatusNormalClosure, "turn done")
	}))
	defer srv.Close()

	var updates []StreamUpdate
	client := &FineStreamClient{
		URL:   "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/interaction/messages/ws?token=abc",
		State: &StreamState{},
		OnFrame: func(u StreamUpdate) {
			updates = append(updates, u)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gotMode != StreamModeFine {
		t.Fatalf("client negotiated stream=%q, want %q", gotMode, StreamModeFine)
	}

	// One update per visible frame: status, started, updated, reasoning,
	// ended, snapshot. The future and foreign-chat frames are silent.
	if len(updates) != 6 {
		t.Fatalf("expected 6 incremental repaints, got %d: %+v", len(updates), updates)
	}
	if !updates[0].StatusChanged {
		t.Fatalf("first repaint should be the status footer: %+v", updates[0])
	}
	if updates[1].ToolCallIndex != 0 || updates[2].ToolCallIndex != 0 || updates[4].ToolCallIndex != 0 {
		t.Fatalf("tool call frames should repaint row 0, got %+v", updates)
	}
	if !updates[3].ReasoningChanged {
		t.Fatalf("reasoning delta should repaint the reasoning row: %+v", updates[3])
	}

	state := client.State
	if state.Status != "calling tools" || state.Reasoning != "thinking" || state.Assistant != "final" {
		t.Fatalf("unexpected view state: %+v", state)
	}
	if len(state.ToolCalls) != 1 || state.ToolCalls[0].ID != "call_1" || !state.ToolCalls[0].Done {
		t.Fatalf("unexpected tool call rows: %+v", state.ToolCalls)
	}
	if line, ok := state.ToolCallLine(0); !ok || !strings.HasPrefix(line, "x coder_read (writing)") {
		t.Fatalf("unexpected rendered row %q ok=%v", line, ok)
	}
}

// A cancelled turn must not surface as a stream error to the user.
func TestFineStreamClientCancelIsNotAnError(t *testing.T) {
	connected := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		close(connected)
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := &FineStreamClient{URL: "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=abc"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()

	<-connected
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled Run returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}
