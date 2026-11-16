package websocket

import (
	"encoding/json"
	"net/url"
	"strings"
)

// StreamMode selects which flavour of non-text stream events a websocket
// subscriber receives. The text channel is already delta shaped
// (`new_partial_message` carries only the chunk and the client appends it), so
// it is identical in both modes; only the non-text channel is split here.
type StreamMode int

const (
	// StreamModeLegacy keeps the historic coarse frames byte for byte. It is
	// the default, so existing clients - including the private TUI - keep
	// working without any change.
	StreamModeLegacy StreamMode = iota
	// StreamModeFine additionally emits the fine grained non-text frames
	// (tool call start/update/end, status, reasoning delta, reconciling
	// snapshot).
	StreamModeFine
)

// QueryParamStream is the query parameter used to negotiate the mode on the
// websocket upgrade request. There is no client -> server frame loop, so the
// mode is negotiated from the URL instead of a `client_hello` round trip.
const QueryParamStream = "stream"

// ParseStreamMode reads the requested stream mode from the upgrade query.
// Anything unknown (or absent) falls back to StreamModeLegacy, which keeps
// existing clients byte for byte compatible.
func ParseStreamMode(q url.Values) StreamMode {
	for _, raw := range q[QueryParamStream] {
		for _, part := range strings.Split(raw, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "fine") {
				return StreamModeFine
			}
		}
	}
	return StreamModeLegacy
}

// ToolCallPhase is the lifecycle phase of a tool call.
type ToolCallPhase string

const (
	ToolCallPhaseStart  ToolCallPhase = "start"
	ToolCallPhaseUpdate ToolCallPhase = "update"
	ToolCallPhaseEnd    ToolCallPhase = "end"
)

// Event type names of the fine grained non-text channel.
const (
	TypeToolCallStarted = "tool_call_started"
	TypeToolCallUpdated = "tool_call_updated"
	TypeToolCallEnded   = "tool_call_ended"
	TypeStatusChanged   = "status_changed"
	TypeReasoningDelta  = "reasoning_delta"
	TypePartialSnapshot = "partial_snapshot"
)

// FineStreamEventTypes lists every frame type the fine channel can emit. It is
// the single source of truth for share censoring and capability checks.
var FineStreamEventTypes = []string{
	TypeToolCallStarted,
	TypeToolCallUpdated,
	TypeToolCallEnded,
	TypeStatusChanged,
	TypeReasoningDelta,
	TypePartialSnapshot,
}

// IsFineStreamEventType reports whether t is a fine grained stream frame.
func IsFineStreamEventType(t string) bool {
	for _, known := range FineStreamEventTypes {
		if known == t {
			return true
		}
	}
	return false
}

// StreamEvent is the envelope shared by every fine grained event. It mirrors
// the legacy envelope shape (`type` + `content`) so a client can dispatch on
// `type` exactly like it already does.
type StreamEvent struct {
	Type    string             `json:"type"`
	Content StreamEventContent `json:"content"`
}

// StreamEventContent carries the routing keys plus an optional opaque payload.
// Seq is only consumed when it is non-zero and only advances on frames that
// were actually emitted, so a legacy client never observes a gap.
type StreamEventContent struct {
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

func buildFineFrame(t string, c StreamEventContent) []byte {
	enc, _ := json.Marshal(StreamEvent{Type: t, Content: c})
	return enc
}

// ToolCallEvent renders one tool call lifecycle frame: started on dispatch,
// updated while it is still running, ended once its status is terminal.
func (m *Messages) ToolCallEvent(phase ToolCallPhase, c StreamEventContent) []byte {
	c.Phase = string(phase)
	switch phase {
	case ToolCallPhaseStart:
		return buildFineFrame(TypeToolCallStarted, c)
	case ToolCallPhaseEnd:
		return buildFineFrame(TypeToolCallEnded, c)
	default:
		return buildFineFrame(TypeToolCallUpdated, c)
	}
}

// StatusEvent renders a phase transition (thinking/responding/tool_call) as an
// incremental frame instead of repeating the phase inside every meta blob.
func (m *Messages) StatusEvent(c StreamEventContent) []byte {
	return buildFineFrame(TypeStatusChanged, c)
}

// ReasoningDeltaEvent renders one reasoning/thinking increment instead of
// re-sending the whole reasoning slice on every chunk.
func (m *Messages) ReasoningDeltaEvent(c StreamEventContent) []byte {
	return buildFineFrame(TypeReasoningDelta, c)
}

// PartialSnapshotEvent renders the throttled reconcile frame. It is emitted
// from the existing streaming-persistence tick, so a fine client that missed
// or dropped a delta re-syncs without a second timer.
func (m *Messages) PartialSnapshotEvent(c StreamEventContent) []byte {
	return buildFineFrame(TypePartialSnapshot, c)
}
