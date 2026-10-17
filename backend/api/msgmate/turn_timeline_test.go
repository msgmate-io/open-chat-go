package msgmate

import "testing"

func TestTurnTimelineOrdersThoughtsAndIndexesToolCalls(t *testing.T) {
	timeline := newTurnTimeline()

	if index := timeline.index(); index != 0 {
		t.Fatalf("fresh timeline index = %d, want 0", index)
	}

	if !timeline.addThought("first thought", 1200) {
		t.Fatal("first thought was not recorded")
	}
	// A tool call dispatched after the first thought sits at index 1.
	if index := timeline.index(); index != 1 {
		t.Fatalf("index after one thought = %d, want 1", index)
	}

	if !timeline.addThought("second thought", 0) {
		t.Fatal("second thought was not recorded")
	}

	entries := timeline.snapshot()
	if len(entries) != 2 {
		t.Fatalf("snapshot length = %d, want 2", len(entries))
	}
	first := entries[0].(map[string]interface{})
	if first["kind"] != timelineKindThought || first["text"] != "first thought" {
		t.Fatalf("unexpected first entry: %#v", first)
	}
	if first["duration_ms"] != int64(1200) {
		t.Fatalf("first entry duration = %#v, want 1200", first["duration_ms"])
	}
	second := entries[1].(map[string]interface{})
	if _, hasDuration := second["duration_ms"]; hasDuration {
		t.Fatalf("an unknown duration must not be reported: %#v", second)
	}
}

func TestTurnTimelineIgnoresDuplicateThoughts(t *testing.T) {
	timeline := newTurnTimeline()
	timeline.addThought("same thought", 10)
	if timeline.addThought("same thought", 10) {
		t.Fatal("duplicate thought was recorded twice")
	}
	if timeline.addThought("", 10) {
		t.Fatal("empty thought was recorded")
	}
	if timeline.len() != 1 {
		t.Fatalf("timeline length = %d, want 1", timeline.len())
	}
}

func TestTurnTimelineStreamsInProgressThought(t *testing.T) {
	timeline := newTurnTimeline()
	timeline.addThought("finalised", 5)

	entries := timeline.snapshotWithStreaming("still thinking")
	if len(entries) != 2 {
		t.Fatalf("snapshot length = %d, want 2", len(entries))
	}
	streaming := entries[1].(map[string]interface{})
	if streaming["streaming"] != true || streaming["text"] != "still thinking" {
		t.Fatalf("unexpected streaming entry: %#v", streaming)
	}

	// Once the block is finalised it is no longer appended as a second entry.
	if got := timeline.snapshotWithStreaming("finalised"); len(got) != 1 {
		t.Fatalf("finalised thought was duplicated: %#v", got)
	}
}

func TestTurnTimelineRecordsAssistantNarration(t *testing.T) {
	timeline := newTurnTimeline()
	timeline.addThought("the build log points at a stale import", 900)

	if !timeline.addText("Let me drop the stale import first.") {
		t.Fatal("narration was not recorded")
	}
	// A tool call dispatched right after the narration sits past both entries.
	if index := timeline.index(); index != 2 {
		t.Fatalf("index after thought + narration = %d, want 2", index)
	}

	entries := timeline.snapshot()
	if len(entries) != 2 {
		t.Fatalf("snapshot length = %d, want 2", len(entries))
	}
	narration := entries[1].(map[string]interface{})
	if narration["kind"] != timelineKindText {
		t.Fatalf("narration kind = %#v, want %q", narration["kind"], timelineKindText)
	}
	if narration["text"] != "Let me drop the stale import first." {
		t.Fatalf("unexpected narration text: %#v", narration["text"])
	}
	if _, hasDuration := narration["duration_ms"]; hasDuration {
		t.Fatalf("narration must not carry a duration: %#v", narration)
	}
}

func TestTurnTimelineCollapsesDuplicateNarration(t *testing.T) {
	timeline := newTurnTimeline()
	if !timeline.addText("same") {
		t.Fatal("first narration was not recorded")
	}
	if timeline.addText("same") {
		t.Fatal("duplicate narration was recorded twice")
	}
	// A different block after it is recorded again.
	if !timeline.addText("different") {
		t.Fatal("a distinct narration block must be recorded")
	}
	if timeline.addText("") {
		t.Fatal("empty narration was recorded")
	}
	if timeline.len() != 2 {
		t.Fatalf("timeline length = %d, want 2", timeline.len())
	}
}

// A running turn streams the timeline into the partial message. Narration
// blocks must be part of it: a tool call's timeline_index counts *all* entries,
// so dropping them would shift every call's position and scramble the ordering.
func TestTurnTimelineStreamingSnapshotKeepsNarration(t *testing.T) {
	timeline := newTurnTimeline()
	timeline.addThought("looking", 10)
	timeline.addText("I will inspect the file now.")

	entries := timeline.snapshotWithStreaming("")
	if len(entries) != 2 {
		t.Fatalf("streamed entries = %d, want 2 (thought + narration)", len(entries))
	}
	if kind := entries[0].(map[string]interface{})["kind"]; kind != timelineKindThought {
		t.Fatalf("first streamed entry kind = %#v, want %q", kind, timelineKindThought)
	}
	if kind := entries[1].(map[string]interface{})["kind"]; kind != timelineKindText {
		t.Fatalf("second streamed entry kind = %#v, want %q", kind, timelineKindText)
	}

	// A block still being produced is appended after the recorded ones.
	withStreaming := timeline.snapshotWithStreaming("still writing")
	if len(withStreaming) != 3 {
		t.Fatalf("streamed entries with an open block = %d, want 3", len(withStreaming))
	}
	if got := withStreaming[2].(map[string]interface{})["streaming"]; got != true {
		t.Fatalf("the open block must be flagged as streaming: %#v", withStreaming[2])
	}
}

func TestTurnTimelineNilIsSafe(t *testing.T) {
	var timeline *turnTimeline
	if timeline.index() != 0 || timeline.len() != 0 {
		t.Fatal("nil timeline must report an empty index")
	}
	if timeline.snapshot() != nil || timeline.snapshotWithStreaming("x") != nil {
		t.Fatal("nil timeline must snapshot to nil")
	}
	if timeline.addThought("x", 1) {
		t.Fatal("nil timeline must not record thoughts")
	}
	if timeline.addText("x") {
		t.Fatal("nil timeline must not record narration")
	}
}
