package chats

import (
	"testing"

	"backend/database"
)

// TestClampMessageListLimitBounds pins the ci#850 page cap: a client-supplied
// `limit` is a request, not an instruction, so anything above the cap is
// lowered to it instead of being passed through to the database.
func TestClampMessageListLimitBounds(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{in: 1, want: 1},
		{in: 40, want: 40},
		{in: MaxMessageListLimit, want: MaxMessageListLimit},
		{in: MaxMessageListLimit + 1, want: MaxMessageListLimit},
		{in: 1_000_000, want: MaxMessageListLimit},
	}
	for _, tc := range cases {
		if got := clampMessageListLimit(tc.in); got != tc.want {
			t.Fatalf("clampMessageListLimit(%d) = %d, want %d (ci#850)", tc.in, got, tc.want)
		}
	}
}

// TestListMessagesCapsOversizedLimit guards that an oversized `limit` cannot
// pull a whole transcript in one request: the response is bounded by
// MaxMessageListLimit and reports the limit it actually applied, so a client
// that pages keeps a stable window instead of silently receiving everything.
func TestListMessagesCapsOversizedLimit(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "limit-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "limit-bot@example.com", false)

	chat := createChatOfType(t, DB, owner, bot, "conversation")
	seedChatMessages(t, DB, chat.ID, owner.ID, bot.ID, MaxMessageListLimit+20)

	capped := listMessagesPage(t, DB, owner, chat.UUID, 100_000, 1)
	if capped.Limit != MaxMessageListLimit {
		t.Fatalf("expected the response to report the applied limit %d, got %d (ci#850)",
			MaxMessageListLimit, capped.Limit)
	}
	if len(capped.Rows) != MaxMessageListLimit {
		t.Fatalf("expected the message list to be capped at %d rows, got %d (ci#850)",
			MaxMessageListLimit, len(capped.Rows))
	}
	if capped.TotalPages < 1 {
		t.Fatalf("expected a positive total_pages for a %d-message chat, got %d",
			MaxMessageListLimit+20, capped.TotalPages)
	}

	// A limit below the cap still passes through untouched.
	small := listMessagesPage(t, DB, owner, chat.UUID, 5, 1)
	if small.Limit != 5 {
		t.Fatalf("expected a request below the cap to be honoured, got limit %d", small.Limit)
	}
	if len(small.Rows) != 5 {
		t.Fatalf("expected 5 rows for limit=5, got %d", len(small.Rows))
	}
}

// TestListSharedInteractionMessagesCapsOversizedLimit is the shared-interaction
// counterpart of the cap: the public message endpoint is the one most exposed to
// an arbitrary `limit`, so it must bound the read too.
func TestListSharedInteractionMessagesCapsOversizedLimit(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "shared-limit-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "shared-limit-bot@example.com", false)

	chat := createChatOfType(t, DB, owner, bot, "interaction")
	seedChatMessages(t, DB, chat.ID, owner.ID, bot.ID, MaxMessageListLimit+20)

	share := database.SharedChatInstance{
		ChatId:        chat.ID,
		OwningUserId:  owner.ID,
		ChatShareUUID: "share-limit-" + chat.UUID,
		ContentMode:   "unsecure",
	}
	if err := DB.Create(&share).Error; err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	capped := listSharedMessagesPage(t, DB, share.ChatShareUUID, 100_000, 1)
	if capped.Limit != MaxMessageListLimit {
		t.Fatalf("expected the shared response to report the applied limit %d, got %d (ci#850)",
			MaxMessageListLimit, capped.Limit)
	}
	if len(capped.Rows) != MaxMessageListLimit {
		t.Fatalf("expected the shared message list to be capped at %d rows, got %d (ci#850)",
			MaxMessageListLimit, len(capped.Rows))
	}
}

// TestMessageListEndpointsShareDeterministicSort guards that both message-list
// endpoints order rows by the same explicit key. A bare `created_at DESC` is not
// deterministic: a batch of messages persisted in the same tick can be returned
// in a different order per page, so a client paging a transcript could see a row
// twice and miss another (ci#850).
func TestMessageListEndpointsShareDeterministicSort(t *testing.T) {
	if messageListSort != "created_at desc, id desc" {
		t.Fatalf("expected the shared message-list sort key, got %q (ci#850)", messageListSort)
	}
}
