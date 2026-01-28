package msgmate

import (
	"context"
	"testing"
)

func TestTrackActiveChatGenerationLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := TrackActiveChatGeneration("chat-1", cancel)
	defer release()

	if !HasActiveChatGeneration("chat-1") {
		t.Fatalf("expected the chat to report an active generation")
	}
	if HasActiveChatGeneration("chat-2") {
		t.Fatalf("unrelated chat must not report an active generation")
	}

	if !CancelActiveChatGeneration("chat-1") {
		t.Fatalf("expected CancelActiveChatGeneration to cancel the tracked generation")
	}
	if ctx.Err() == nil {
		t.Fatalf("expected the tracked context to be cancelled")
	}

	release()
	if HasActiveChatGeneration("chat-1") {
		t.Fatalf("expected release to clear the registry entry")
	}
	if CancelActiveChatGeneration("chat-1") {
		t.Fatalf("cancelling an untracked chat must report false")
	}
}

func TestTrackActiveChatGenerationSupersedesPrevious(t *testing.T) {
	firstCtx, firstCancel := context.WithCancel(context.Background())
	releaseFirst := TrackActiveChatGeneration("chat-1", firstCancel)

	secondCtx, secondCancel := context.WithCancel(context.Background())
	releaseSecond := TrackActiveChatGeneration("chat-1", secondCancel)
	defer releaseSecond()

	if firstCtx.Err() == nil {
		t.Fatalf("a newer generation must cancel the superseded one")
	}
	if secondCtx.Err() != nil {
		t.Fatalf("the newer generation must stay live")
	}

	// The superseded generation's release must not clear the newer entry.
	releaseFirst()
	if !HasActiveChatGeneration("chat-1") {
		t.Fatalf("releasing a superseded generation must not clear the newer one")
	}
}
