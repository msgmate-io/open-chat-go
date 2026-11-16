package tui_test

// Contract guard for ci#1104.
//
// The TUI client keeps its own copy of the fine grained stream contract
// (backend/tui/stream.go) instead of importing backend/api/websocket, because
// the TUI model package is built into the private client and must not drag the
// server's database stack along. A copy can drift, so this test pins the copy
// against the server package: every mirrored event type name and the
// `?stream=fine` negotiation have to agree, otherwise the TUI silently stops
// receiving the frames it renders live.

import (
	"net/url"
	"testing"

	"backend/api/websocket"
	"backend/tui"
)

func TestFineStreamContractMatchesServer(t *testing.T) {
	if tui.QueryParamStream != websocket.QueryParamStream {
		t.Fatalf("query parameter drifted: tui=%q server=%q", tui.QueryParamStream, websocket.QueryParamStream)
	}

	serverTypes := map[string]struct{}{}
	for _, name := range websocket.FineStreamEventTypes {
		serverTypes[name] = struct{}{}
	}
	clientTypes := map[string]struct{}{}
	for _, name := range tui.FineStreamEventTypes {
		clientTypes[name] = struct{}{}
		if _, ok := serverTypes[name]; !ok {
			t.Errorf("tui mirrors event type %q that the server never emits", name)
		}
		if !tui.IsFineEventType(name) {
			t.Errorf("IsFineEventType(%q) is false for a mirrored type", name)
		}
	}
	for name := range serverTypes {
		if _, ok := clientTypes[name]; !ok {
			t.Errorf("server emits %q but the TUI does not render it", name)
		}
	}
}

// TestStreamURLNegotiatesTheModeTheServerParses feeds the URL the client builds
// back through the server parser: if the TUI asked for a mode the server reads
// as legacy, the live tool call rows would stay empty.
func TestStreamURLNegotiatesTheModeTheServerParses(t *testing.T) {
	raw := "wss://api.msgmate.io/api/v1/interaction/messages/ws?token=abc"
	negotiated, err := url.Parse(tui.StreamURL(raw))
	if err != nil {
		t.Fatalf("parse negotiated url: %v", err)
	}
	if got, want := websocket.ParseStreamMode(negotiated.Query()), websocket.StreamModeFine; got != want {
		t.Fatalf("server parsed mode %v, want %v (query %q)", got, want, negotiated.RawQuery)
	}
	if got := negotiated.Query().Get("token"); got != "abc" {
		t.Fatalf("auth token must survive the negotiation, got %q", got)
	}
}
