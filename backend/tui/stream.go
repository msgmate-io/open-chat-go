package tui

import (
	"encoding/json"
	"net/url"
	"strings"
)

// This file is the client half of the fine grained websocket stream (ci#1104).
//
// The core server can negotiate a fine grained non-text channel with
// `?stream=fine` (see backend/api/websocket/finegrained.go). Without the
// negotiation the TUI only ever sees coarse frames and therefore cannot show
// live tool calls. The TUI integration owns the socket and the drawing, but the
// URL negotiation, the frame contract and the incremental view state must not
// drift between core and integration, so they live here as data plus one pure
// state machine.

// QueryParamStream is the upgrade query parameter that selects the stream mode.
// It mirrors websocket.QueryParamStream.
const QueryParamStream = "stream"

// StreamModeFine is the value that switches on the fine grained channel.
const StreamModeFine = "fine"

// Fine event type names, mirrored from the server contract.
const (
	TypeToolCallStarted = "tool_call_started"
	TypeToolCallUpdated = "tool_call_updated"
	TypeToolCallEnded   = "tool_call_ended"
	TypeStatusChanged   = "status_changed"
	TypeReasoningDelta  = "reasoning_delta"
	TypePartialSnapshot = "partial_snapshot"
)

// FineStreamEventTypes lists every frame type the fine channel can emit,
// mirrored from the server contract.
var FineStreamEventTypes = []string{
	TypeToolCallStarted,
	TypeToolCallUpdated,
	TypeToolCallEnded,
	TypeStatusChanged,
	TypeReasoningDelta,
	TypePartialSnapshot,
}

// IsFineEventType reports whether t is a fine grained stream frame, so the TUI
// can skip the coarse frames a legacy socket keeps sending.
func IsFineEventType(t string) bool {
	for _, known := range FineStreamEventTypes {
		if known == t {
			return true
		}
	}
	return false
}

// Event is one frame of the fine grained channel. It is the same envelope the
// server emits (`type` plus `content`), so a frame can be decoded straight into
// it and handed to StreamState.Apply.
type Event struct {
	Type    string       `json:"type"`
	Content EventContent `json:"content"`
}

// EventContent carries the routing keys plus the optional per type payload.
type EventContent struct {
	ChatUUID   string                 `json:"chat_uuid"`
	SenderUUID string                 `json:"sender_uuid"`
	SessionID  string                 `json:"session_id,omitempty"`
	Seq        int64                  `json:"seq,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	Phase      string                 `json:"phase,omitempty"`
	Delta      string                 `json:"delta,omitempty"`
	Status     string                 `json:"status,omitempty"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

// StreamURL rewrites an already authenticated interaction websocket URL so it
// asks for the fine grained channel. It keeps every existing query parameter
// (the token of the coder-tui audience lives there) and only adds stream=fine,
// so the caller does not have to rebuild its auth. An unparsable URL is
// returned unchanged rather than silently opening a legacy socket.
func StreamURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	q := u.Query()
	if strings.EqualFold(q.Get(QueryParamStream), StreamModeFine) {
		return u.String()
	}
	q.Set(QueryParamStream, StreamModeFine)
	u.RawQuery = q.Encode()
	return u.String()
}

// ToolCall is one live tool call row of the TUI transcript.
type ToolCall struct {
	ID      string         `json:"id"`
	Name    string         `json:"name,omitempty"`
	Status  string         `json:"status,omitempty"`
	Summary string         `json:"summary,omitempty"`
	Done    bool           `json:"done,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

// StreamState is the TUI's incremental view of one streamed interaction turn.
// Apply mutates it in place: a frame touches only the row it names plus the
// counter that tells the renderer what to repaint, so the TUI never redraws the
// whole transcript array per frame.
type StreamState struct {
	// ChatUUID binds the state to one chat; frames of other chats are
	// ignored. It is bound by the first frame that names one.
	ChatUUID string
	// Status is the current turn status shown in the footer.
	Status string
	// Reasoning accumulates reasoning deltas; it is never rewritten by a
	// delta, only replaced by a partial snapshot.
	Reasoning string
	// Assistant is the in-flight assistant text, reconciled by snapshots.
	Assistant string
	// ToolCalls are the tool call rows in first-seen order. Rows are updated
	// by ID, never reordered or rebuilt, so the index of a row is stable for
	// the lifetime of the turn.
	ToolCalls []ToolCall
}

// StreamUpdate reports what one frame changed, so the renderer can repaint the
// affected rows instead of the entire transcript.
type StreamUpdate struct {
	// ToolCallIndex is the index into ToolCalls that changed, or -1.
	ToolCallIndex int
	// ReasoningChanged and AssistantChanged flag the transcript text rows.
	ReasoningChanged bool
	AssistantChanged bool
	// StatusChanged flags the footer row.
	StatusChanged bool
	// Snapshot marks a reconciling full text replacement.
	Snapshot bool
}

// None reports whether the frame changed no visible state.
func (u StreamUpdate) None() bool {
	return u.ToolCallIndex < 0 && !u.ReasoningChanged && !u.AssistantChanged && !u.StatusChanged
}

// Apply folds one fine grained frame into the state. Unknown frames and frames
// of another chat are ignored, so a legacy socket that sends coarse frames can
// still be fed in safely.
func (s *StreamState) Apply(raw []byte) (StreamUpdate, error) {
	update := StreamUpdate{ToolCallIndex: -1}
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return update, err
	}
	c := event.Content
	if c.ChatUUID != "" {
		if s.ChatUUID == "" {
			// Auto-bind on the first frame so callers do not have to
			// thread the chat id through just to filter frames.
			s.ChatUUID = c.ChatUUID
		} else if !strings.EqualFold(s.ChatUUID, c.ChatUUID) {
			return update, nil
		}
	}
	if !IsFineEventType(event.Type) {
		return update, nil
	}
	switch event.Type {
	case TypeStatusChanged:
		if c.Status == "" || c.Status == s.Status {
			return update, nil
		}
		s.Status = c.Status
		update.StatusChanged = true
	case TypeReasoningDelta:
		if c.Delta == "" {
			return update, nil
		}
		s.Reasoning += c.Delta
		update.ReasoningChanged = true
	case TypePartialSnapshot:
		// The server's payload shape is owned by core, so accept every
		// spelling the reconciling snapshot has used instead of pinning
		// the client to one key and silently dropping the resync.
		text := firstPayloadString(c.Payload, "content", "text", "assistant", "message")
		reasoning := firstPayloadString(c.Payload, "reasoning", "thinking")
		update.Snapshot = true
		if text != "" && text != s.Assistant {
			s.Assistant = text
			update.AssistantChanged = true
		}
		if reasoning != "" && reasoning != s.Reasoning {
			s.Reasoning = reasoning
			update.ReasoningChanged = true
		}
	case TypeToolCallStarted, TypeToolCallUpdated, TypeToolCallEnded:
		if c.ToolCallID == "" {
			return update, nil
		}
		update.ToolCallIndex = s.upsertToolCall(c, event.Type == TypeToolCallEnded)
	}
	return update, nil
}

// upsertToolCall updates the row named by the frame, appending it on first
// sight. It returns the row index so the caller repaints one line.
func (s *StreamState) upsertToolCall(c EventContent, done bool) int {
	for i := range s.ToolCalls {
		if s.ToolCalls[i].ID != c.ToolCallID {
			continue
		}
		tc := &s.ToolCalls[i]
		if name := payloadString(c.Payload, "name"); name != "" {
			tc.Name = name
		}
		if summary := payloadString(c.Payload, "summary"); summary != "" {
			tc.Summary = summary
		}
		if c.Status != "" {
			tc.Status = c.Status
		}
		if done {
			tc.Done = true
			if tc.Status == "" {
				tc.Status = "done"
			}
		}
		if c.Payload != nil {
			tc.Payload = c.Payload
		}
		return i
	}
	tc := ToolCall{
		ID:      c.ToolCallID,
		Name:    payloadString(c.Payload, "name"),
		Summary: payloadString(c.Payload, "summary"),
		Status:  c.Status,
		Done:    done,
		Payload: c.Payload,
	}
	s.ToolCalls = append(s.ToolCalls, tc)
	return len(s.ToolCalls) - 1
}

// Line renders one transcript line of the tool call, the way the TUI draws it
// while the turn is still running. It is deterministic and free of terminal
// width logic so the renderer can repaint a single row.
func (t ToolCall) Line() string {
	name := t.Name
	if name == "" {
		name = t.ID
	}
	status := t.Status
	if status == "" {
		status = "running"
		if t.Done {
			status = "done"
		}
	}
	marker := ">"
	if t.Done {
		marker = "x"
	}
	line := marker + " " + name + " (" + status + ")"
	if t.Summary != "" {
		line += ": " + t.Summary
	}
	return line
}

// ToolCallLine renders row i for an incremental repaint. The second result is
// false when i names no row, which lets the renderer ignore a stale update.
func (s *StreamState) ToolCallLine(i int) (string, bool) {
	if i < 0 || i >= len(s.ToolCalls) {
		return "", false
	}
	return s.ToolCalls[i].Line(), true
}

// firstPayloadString returns the first non-empty string value among keys.
func firstPayloadString(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := payloadString(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func payloadString(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return value
}
