package chats

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func decodeListedChats(t *testing.T, rr *httptest.ResponseRecorder) ListedChatsPage {
	t.Helper()
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var page ListedChatsPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	return page
}

func listedChatByUUID(page ListedChatsPage, uuid string) *ListedChat {
	for i := range page.Rows {
		if page.Rows[i].UUID == uuid {
			return &page.Rows[i]
		}
	}
	return nil
}

// TestListIncludesPinnedChatAgedOutOfFirstPage guards ci#512: a pinned chat
// that has become so old it is no longer among the most recent rows must still
// be returned on the first page as a pin, instead of silently disappearing.
func TestListIncludesPinnedChatAgedOutOfFirstPage(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-pin-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-pin-bot@example.com", false)

	// The pinned chat is the oldest row; newer chats push it out of the page.
	oldest := createChatOfType(t, DB, owner, bot, "conversation")
	if err := setChatPin(DB, oldest.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the oldest chat: %v", err)
	}
	for i := 0; i < 3; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=2", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	pinned := listedChatByUUID(page, oldest.UUID)
	if pinned == nil {
		t.Fatal("pinned chat aged out of the first page and disappeared (ci#512)")
	}
	if !pinned.Pinned {
		t.Fatal("expected the aged-out pinned chat to be flagged pinned")
	}

	occurrences := 0
	for _, row := range page.Rows {
		if row.UUID == oldest.UUID {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("expected the pinned chat exactly once, got %d", occurrences)
	}
}

// TestListPinnedTopUpRespectsActiveFilters guards that the pinned top-up does
// not surface a pinned chat that the active server-side filters exclude, so a
// filtered list stays filtered.
func TestListPinnedTopUpRespectsActiveFilters(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-filter-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-filter-bot@example.com", false)

	pinnedConversation := createChatOfType(t, DB, owner, bot, "conversation")
	if err := setChatPin(DB, pinnedConversation.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the conversation chat: %v", err)
	}
	for i := 0; i < 3; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=2&chat_types=interaction", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	if listedChatByUUID(page, pinnedConversation.UUID) != nil {
		t.Fatal("pinned top-up leaked a chat excluded by the active chat_types filter")
	}
}

// TestListPinnedChatAlreadyInPageNotDuplicated guards that a pinned chat that
// is already part of the page is not returned twice by the top-up.
func TestListPinnedChatAlreadyInPageNotDuplicated(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "list-dedupe-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "list-dedupe-bot@example.com", false)

	newest := createChatOfType(t, DB, owner, bot, "conversation")
	if err := setChatPin(DB, newest.ID, owner.ID, true); err != nil {
		t.Fatalf("failed to pin the newest chat: %v", err)
	}
	for i := 0; i < 2; i++ {
		createChatOfType(t, DB, owner, bot, "conversation")
	}

	req := httptest.NewRequest("GET", "/api/v1/chats/list?page=1&limit=40", nil)
	req = withActionTasksUser(req, DB, owner)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.List(rr, req)

	page := decodeListedChats(t, rr)
	occurrences := 0
	for _, row := range page.Rows {
		if row.UUID == newest.UUID {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("expected the in-page pinned chat exactly once, got %d", occurrences)
	}
	if row := listedChatByUUID(page, newest.UUID); row == nil || !row.Pinned {
		t.Fatal("expected the in-page pinned chat to be flagged pinned")
	}
}
