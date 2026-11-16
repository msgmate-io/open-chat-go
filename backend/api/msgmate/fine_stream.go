package msgmate

import (
	"strings"

	wsapi "backend/api/websocket"
)

// fineStreamEmitter publishes the fine grained non-text stream frames of
// ci#1104 next to the legacy `new_partial_message` frames.
//
// The text channel is already delta shaped (the partial frame carries only the
// chunk and the client appends it), so it is deliberately NOT touched here.
// Only the coarse non-text channel is split: tool call start/update/end,
// status/phase transitions, reasoning deltas and a throttled reconcile
// snapshot.
//
// Every emit is additive: the legacy frame is still sent unchanged, and the
// fine frames only reach subscribers that negotiated `?stream=fine` on the
// websocket upgrade. A legacy client (including the private TUI) therefore
// observes exactly the historic frame set.
type fineStreamEmitter struct {
	handler    *wsapi.WebSocketHandler
	chatUUID   string
	senderUUID string
	sessionID  string
	seq        func() int64
	lastPhase  string
	// sink is an optional test seam: when set, frames are handed to it instead
	// of the websocket handler so the emitted frame set can be asserted without
	// a live subscriber.
	sink func([]byte)
}

// newFineStreamEmitter returns an emitter bound to one streaming turn. It is
// safe to call on a nil handler: every emit then becomes a no-op.
func newFineStreamEmitter(handler *wsapi.WebSocketHandler, chatUUID, senderUUID, sessionID string, seq func() int64) *fineStreamEmitter {
	if handler == nil {
		return nil
	}
	return &fineStreamEmitter{
		handler:    handler,
		chatUUID:   chatUUID,
		senderUUID: senderUUID,
		sessionID:  sessionID,
		seq:        seq,
	}
}

func (e *fineStreamEmitter) content() wsapi.StreamEventContent {
	c := wsapi.StreamEventContent{
		ChatUUID:   e.chatUUID,
		SenderUUID: e.senderUUID,
		SessionID:  e.sessionID,
	}
	if e.seq != nil {
		c.Seq = e.seq()
	}
	return c
}

// messages returns the frame renderer of the bound websocket handler. It is
// nil when the emitter has no handler (tests, CLI paths) or when the handler was
// built without a renderer (a struct literal), so every emit becomes a no-op
// instead of dereferencing a nil field. The dedupe state of status is advanced
// before this check, so the phase tracking stays correct either way.
func (e *fineStreamEmitter) messages() *wsapi.Messages {
	if e == nil || e.handler == nil {
		return nil
	}
	return e.handler.MessageHandler
}

func (e *fineStreamEmitter) publish(frame []byte) {
	if e == nil || len(frame) == 0 {
		return
	}
	if e.sink != nil {
		e.sink(frame)
		return
	}
	if e.handler == nil {
		return
	}
	e.handler.PublishFineInChannel(frame, e.senderUUID)
	e.handler.PublishFineInChatChannel(frame, e.chatUUID)
}

// toolCall emits one tool call lifecycle frame. The phase is derived from the
// tool status: a call that is still ongoing is an update, a terminal status
// ends it, and a call seen for the first time starts it.
func (e *fineStreamEmitter) toolCall(toolCallID, toolName, status string, firstSeen bool, payload map[string]interface{}) {
	if e == nil {
		return
	}
	messages := e.messages()
	if messages == nil {
		return
	}
	phase := toolCallPhase(status, firstSeen)
	c := e.content()
	c.ToolCallID = toolCallID
	c.Status = status
	if toolName != "" {
		if c.Payload == nil {
			c.Payload = map[string]interface{}{}
		}
		c.Payload["tool_call"] = toolName
	}
	for key, value := range payload {
		if c.Payload == nil {
			c.Payload = map[string]interface{}{}
		}
		c.Payload[key] = value
	}
	e.publish(messages.ToolCallEvent(phase, c))
}

// status emits a phase transition (thinking / responding / tool_call) as an
// incremental frame instead of repeating the phase inside every meta blob.
// Repeated calls with the same phase are suppressed, so a fine client sees one
// frame per transition rather than one per chunk.
func (e *fineStreamEmitter) status(phase string) {
	if e == nil || phase == "" {
		return
	}
	if e.lastPhase == phase {
		return
	}
	e.lastPhase = phase
	messages := e.messages()
	if messages == nil {
		return
	}
	c := e.content()
	c.Status = phase
	e.publish(messages.StatusEvent(c))
}

// reasoning emits one reasoning/thinking increment instead of re-sending the
// whole reasoning slice on every chunk.
func (e *fineStreamEmitter) reasoning(delta string) {
	if e == nil || delta == "" {
		return
	}
	messages := e.messages()
	if messages == nil {
		return
	}
	c := e.content()
	c.Delta = delta
	e.publish(messages.ReasoningDeltaEvent(c))
}

// snapshot emits the reconcile frame. It is driven by the existing 1s
// streaming-persistence tick, so a fine client that missed or dropped a delta
// re-syncs without a second timer.
func (e *fineStreamEmitter) snapshot(text string, reasoning []string, toolCalls []interface{}) {
	if e == nil {
		return
	}
	messages := e.messages()
	if messages == nil {
		return
	}
	c := e.content()
	c.Delta = text
	if c.Payload == nil {
		c.Payload = map[string]interface{}{}
	}
	c.Payload["reasoning"] = reasoning
	c.Payload["tool_calls"] = toolCalls
	e.publish(messages.PartialSnapshotEvent(c))
}

// toolCallPhase derives the lifecycle phase of a tool call frame: a call seen
// for the first time starts, a terminal status ends it, everything else is an
// update.
func toolCallPhase(status string, firstSeen bool) wsapi.ToolCallPhase {
	switch {
	case firstSeen:
		return wsapi.ToolCallPhaseStart
	case isTerminalToolStatus(status):
		return wsapi.ToolCallPhaseEnd
	default:
		return wsapi.ToolCallPhaseUpdate
	}
}

// isTerminalToolStatus reports whether a tool status ends the call.
func isTerminalToolStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case ToolCallStatusSucceeded, ToolCallStatusFailed:
		return true
	}
	return false
}
