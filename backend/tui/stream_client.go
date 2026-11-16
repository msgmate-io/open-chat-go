package tui

import (
	"context"

	"github.com/coder/websocket"
)

// This file is the client half of ci#1104: the part that actually connects the
// TUI to the fine grained channel and streams the frames into the view.
//
// backend/tui/stream.go owns the URL negotiation and the frame contract;
// without a consumer the TUI never asked for `?stream=fine` and only ever saw
// the coarse legacy frames, so the user never observed live tool calls. The
// private TUI integration owns the terminal, the keys and the socket library
// choice, so it plugs its own dialer in and receives one incremental update per
// frame to repaint.

// defaultReadLimit bounds one websocket frame. Reasoning deltas and snapshots
// are small, but a snapshot carries the accumulated text of a long turn.
const defaultReadLimit = 1 << 20

// FrameFunc receives the incremental update of one consumed frame. The renderer
// repaints exactly what the update names (one tool call row, the reasoning row,
// the assistant row or the footer), never the whole transcript array.
type FrameFunc func(StreamUpdate)

// FineStreamClient is the long lived consumer of one interaction stream.
//
// The integration dials the authenticated interaction websocket through Dial
// (or the default dialer) and calls Run: the client negotiates `?stream=fine`
// on that already authenticated URL, decodes every frame and folds it into
// State. Transport failures are terminal, undecodable frames are not: a newer
// server may send a frame type this client does not know yet.
type FineStreamClient struct {
	// URL is the authenticated interaction websocket URL (ws:// or wss://).
	// Its existing query - the token lives there - is preserved and only the
	// stream mode is added.
	URL string
	// State is the incremental view frames are folded into. A nil State
	// starts a fresh one.
	State *StreamState
	// OnFrame is called for every frame that changed visible state, in
	// arrival order. It may be nil for a headless consumer.
	OnFrame FrameFunc
	// Dial overrides the websocket dialer (tests, proxies, integrations
	// that already own a connection). It defaults to websocket.Dial.
	Dial func(ctx context.Context, url string) (*websocket.Conn, error)
}

// Run consumes the fine grained stream until the server closes the socket or
// ctx is cancelled, and then closes the connection.
//
// A normal close and a cancelled context both return nil, so a cancelled turn
// is not reported to the user as a stream failure.
func (c *FineStreamClient) Run(ctx context.Context) error {
	if c.State == nil {
		c.State = &StreamState{}
	}
	dial := c.Dial
	if dial == nil {
		dial = func(ctx context.Context, rawURL string) (*websocket.Conn, error) {
			conn, _, err := websocket.Dial(ctx, rawURL, nil)
			if err != nil {
				return nil, err
			}
			// One turn can keep the socket open across a long tool call, so
			// do not let a single snapshot hit the default frame limit.
			conn.SetReadLimit(defaultReadLimit)
			return conn, nil
		}
	}

	conn, err := dial(ctx, StreamURL(c.URL))
	if err != nil {
		if ctx.Err() != nil {
			// The turn was cancelled while the socket was still
			// handshaking: that is a cancellation, not a stream failure.
			return nil
		}
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil || websocket.CloseStatus(err) >= 0 {
				return nil
			}
			return err
		}
		update, err := c.State.Apply(data)
		if err != nil {
			// Unknown or malformed frame: keep the stream alive rather
			// than killing the live view mid-turn.
			continue
		}
		if update.None() || c.OnFrame == nil {
			continue
		}
		c.OnFrame(update)
	}
}
