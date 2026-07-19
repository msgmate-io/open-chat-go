package workqueue

import "testing"

func TestChatUUIDFromBotReplyTaskID(t *testing.T) {
	cases := []struct {
		id   string
		want string
		ok   bool
	}{
		{"bot-reply:abc", "abc", true},
		{"bot-reply:abc:def", "abc", true},
		{"bot-reply:", "", false},
		{"other-task:abc", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := chatUUIDFromBotReplyTaskID(tc.id)
		if got != tc.want || ok != tc.ok {
			t.Errorf("chatUUIDFromBotReplyTaskID(%q) = (%q,%v), want (%q,%v)", tc.id, got, ok, tc.want, tc.ok)
		}
	}
}

// TestBotReplyActivitySnapshotSemantics pins the snapshot's answers to the same
// rules as the per-chat helpers: a chat is only "queued" when it is also active,
// and the base task ID is not counted as a queued reply (ci#650/ci#656/ci#850).
func TestBotReplyActivitySnapshotSemantics(t *testing.T) {
	snapshot := &BotReplyActivity{
		active:  map[string]bool{"live": true},
		pending: map[string]int{"live": 2, "idle": 3},
	}

	if !snapshot.IsActive("live") {
		t.Fatalf("expected 'live' active")
	}
	if snapshot.IsActive("idle") {
		t.Fatalf("expected 'idle' inactive")
	}
	if got := snapshot.QueuedCount("live"); got != 2 {
		t.Fatalf("QueuedCount(live) = %d, want 2", got)
	}
	// A chat with pending work but no live turn reports 0 queued replies, like
	// BotReplyQueuedCount.
	if got := snapshot.QueuedCount("idle"); got != 0 {
		t.Fatalf("QueuedCount(idle) = %d, want 0", got)
	}

	var nilSnapshot *BotReplyActivity
	if nilSnapshot.IsActive("live") || nilSnapshot.QueuedCount("live") != 0 {
		t.Fatalf("nil snapshot must be inert")
	}
}
