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
}
