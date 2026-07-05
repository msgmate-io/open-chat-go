package chats

import (
	"backend/database"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"
)

// TestListMessagesPageCountIsScopedToTheChat guards that the pagination of a
// chat's message list counts only that chat's messages.
//
// database.Paginate derives TotalPages by running a Count on the *handle it is
// given*. Handlers that passed the bare DB and only added their
// `Where("chat_id = ?", ...)` to the scopes chain left the count unfiltered, so
// every message in the database inflated the page count of every chat. A chat
// with three messages then advertised hundreds of pages and clients happily
// kept paging into empty pages — the unnecessary-fetch/404 class tracked as
// ci#789.
func TestListMessagesPageCountIsScopedToTheChat(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "page-count-owner@example.com", false)
	partner := createUserForChatsTest(t, DB, "page-count-partner@example.com", false)

	smallChat := createChatOfType(t, DB, owner, partner, "conversation")
	bigChat := createChatOfType(t, DB, owner, partner, "conversation")

	seedChatMessages(t, DB, smallChat.ID, owner.ID, partner.ID, 3)
	seedChatMessages(t, DB, bigChat.ID, owner.ID, partner.ID, 20)

	h := &ChatsHandler{}

	// limit=2 over 3 messages: 2 pages, second one partial.
	first := h.listMessagesPage(t, DB, owner, smallChat.UUID, 1, 2)
	if first.TotalPages != 2 {
		t.Fatalf("expected total_pages 2 for a 3-message chat with limit 2, got %d "+
			"(the count must not see the sibling chat's %d messages)", first.TotalPages, 20)
	}
	if len(first.Rows) != 2 {
		t.Fatalf("expected 2 rows on the first page, got %d", len(first.Rows))
	}

	second := h.listMessagesPage(t, DB, owner, smallChat.UUID, 2, 2)
	if second.TotalPages != 2 {
		t.Fatalf("expected total_pages 2 on the last page, got %d", second.TotalPages)
	}
	if len(second.Rows) != 1 {
		t.Fatalf("expected the partial last page to hold 1 row, got %d", len(second.Rows))
	}

	// Paging past the end stays empty, but the advertied page count must not
	// grow to match the database-wide message count.
	past := h.listMessagesPage(t, DB, owner, smallChat.UUID, 5, 2)
	if past.TotalPages != 2 {
		t.Fatalf("expected total_pages to stay 2 past the end of the chat, got %d", past.TotalPages)
	}
	if len(past.Rows) != 0 {
		t.Fatalf("expected no rows past the end of the chat, got %d", len(past.Rows))
	}

	// The big chat's own page count must still be correct, i.e. the fix scopes
	// the count rather than replacing it with something constant.
	big := h.listMessagesPage(t, DB, owner, bigChat.UUID, 1, 2)
	if big.TotalPages != 10 {
		t.Fatalf("expected total_pages 10 for the 20-message chat, got %d", big.TotalPages)
	}
	if len(big.Rows) != 2 {
		t.Fatalf("expected 2 rows on the big chat's first page, got %d", len(big.Rows))
	}
}

func seedChatMessages(t *testing.T, DB *gorm.DB, chatID, senderID, receiverID uint, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		text := fmt.Sprintf("message %d", i)
		msg := database.Message{
			ChatId:     chatID,
			SenderId:   senderID,
			ReceiverId: receiverID,
			DataType:   "text",
			Text:       &text,
		}
		if err := DB.Create(&msg).Error; err != nil {
			t.Fatalf("failed to create message %d for chat %d: %v", i, chatID, err)
		}
	}
}

func (h *ChatsHandler) listMessagesPage(t *testing.T, DB *gorm.DB, user *database.User, chatUUID string, page, limit int) ListedMessagesPage {
	t.Helper()
	url := fmt.Sprintf("/api/v1/chats/%s/messages/list?page=%d&limit=%d", chatUUID, page, limit)
	req := httptest.NewRequest("GET", url, nil)
	req.SetPathValue("chat_uuid", chatUUID)
	req = withActionTasksUser(req, DB, user)

	rr := httptest.NewRecorder()
	h.ListMessages(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200 listing messages (page %d), got %d: %s", page, rr.Code, rr.Body.String())
	}

	var response ListedMessagesPage
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode listed messages page: %v", err)
	}
	return response
}
