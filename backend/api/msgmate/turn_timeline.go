package msgmate

// The turn timeline (ci#569) is the ordered narration of a bot turn: the
// assistant's reasoning/thought blocks in the order the model produced them.
// Every tool call records how many timeline entries had already been emitted
// when it was dispatched (`timeline_index`), so the chat UI can render the
// reasoning *between* the tool calls instead of showing only opaque call rows.
//
// The timeline is streamed in the partial-message meta_data while the turn is
// running and persisted in the final message's meta_data as
//
//	"timeline": [
//	  {"kind": "thought", "text": "...", "duration_ms": 1200},
//	  ...
//	]
//
// The type is pure so the ordering can be unit-tested without a stream.

const (
	// timelineKindThought is the meta_data.timeline entry kind the chat UI
	// renders as a reasoning block.
	timelineKindThought = "thought"
)

// turnTimeline accumulates the ordered reasoning blocks of one bot turn.
type turnTimeline struct {
	entries  []map[string]interface{}
	thoughts map[string]struct{}
}

func newTurnTimeline() *turnTimeline {
	return &turnTimeline{thoughts: make(map[string]struct{})}
}

// addThought records a reasoning block. The same thought can be reported twice
// (once as a streamed thinking step and once as a think tag inside the
// assistant text), so an already recorded block is ignored. It reports whether
// the entry was added.
func (t *turnTimeline) addThought(text string, durationMs int64) bool {
	if t == nil {
		return false
	}
	if text == "" {
		return false
	}
	if _, seen := t.thoughts[text]; seen {
		return false
	}
	t.thoughts[text] = struct{}{}
	entry := map[string]interface{}{
		"kind": timelineKindThought,
		"text": text,
	}
	if durationMs > 0 {
		entry["duration_ms"] = durationMs
	}
	t.entries = append(t.entries, entry)
	return true
}

// index returns the number of entries recorded so far, ie the position in the
// timeline a tool call dispatched right now sits at.
func (t *turnTimeline) index() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}

// len returns the number of recorded entries.
func (t *turnTimeline) len() int {
	return t.index()
}

// snapshot returns the entries in order as JSON-friendly values.
func (t *turnTimeline) snapshot() []interface{} {
	if t == nil || len(t.entries) == 0 {
		return nil
	}
	entries := make([]interface{}, 0, len(t.entries))
	for _, entry := range t.entries {
		entries = append(entries, entry)
	}
	return entries
}

// snapshotWithStreaming returns the entries in order plus the reasoning block
// that is currently still streaming (`inProgress`), flagged as such so the chat
// UI can render it as it arrives. A block that was already recorded is not
// duplicated.
func (t *turnTimeline) snapshotWithStreaming(inProgress string) []interface{} {
	if t == nil {
		return nil
	}
	entries := t.snapshot()
	if inProgress == "" {
		return entries
	}
	if _, seen := t.thoughts[inProgress]; seen {
		return entries
	}
	streaming := map[string]interface{}{
		"kind":      timelineKindThought,
		"text":      inProgress,
		"streaming": true,
	}
	if entries == nil {
		return []interface{}{streaming}
	}
	return append(entries, streaming)
}
