package chats

import (
	"backend/database"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"gorm.io/gorm"
)

// createMessageForChatsTest inserts a plain text message into a chat.
func createMessageForChatsTest(t *testing.T, DB *gorm.DB, chat database.Chat, sender, receiver *database.User, text string) database.Message {
	t.Helper()
	body := text
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   sender.ID,
		ReceiverId: receiver.ID,
		DataType:   "text",
		Text:       &body,
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}
	return message
}

func listMessagesPage(t *testing.T, DB *gorm.DB, user *database.User, chatUUID string, limit, page int) ListedMessagesPage {
	t.Helper()
	target := "/api/v1/chats/" + chatUUID + "/messages/list?limit=" + strconv.Itoa(limit) + "&page=" + strconv.Itoa(page)
	req := httptest.NewRequest("GET", target, nil)
	req.SetPathValue("chat_uuid", chatUUID)
	req = withActionTasksUser(req, DB, user)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.ListMessages(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected status 200 for page %d, got %d: %s", page, rr.Code, rr.Body.String())
	}
	var listed ListedMessagesPage
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("failed to decode message list response: %v", err)
	}
	return listed
}

func listSharedMessagesPage(t *testing.T, DB *gorm.DB, shareUUID string, limit, page int) ListedMessagesPage {
	t.Helper()
	target := "/api/interaction/" + shareUUID + "/messages?limit=" + strconv.Itoa(limit) + "&page=" + strconv.Itoa(page)
	req := httptest.NewRequest("GET", target, nil)
	req.SetPathValue("chat_share_uuid", shareUUID)
	req = withActionTasksUser(req, DB, nil)
	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.ListSharedInteractionMessages(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected status 200 for page %d, got %d: %s", page, rr.Code, rr.Body.String())
	}
	var listed ListedMessagesPage
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("failed to decode shared message list response: %v", err)
	}
	return listed
}

// TestListMessagesTotalPagesIsChatScoped guards ci#825: total_pages must be
// derived from the requested chat's messages, not from every row in the shared
// messages table. With unrelated chats holding hundreds of messages, a
// three-message chat with limit=2 reports 2 pages, the last page is partial and
// paging past the end stays empty with a stable total_pages.
func TestListMessagesTotalPagesIsChatScoped(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "paging-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "paging-bot@example.com", false)

	chat := createChatOfType(t, DB, owner, bot, "conversation")
	for i := 0; i < 3; i++ {
		createMessageForChatsTest(t, DB, chat, owner, bot, "mine")
	}

	// Unrelated rows in the same table: an unscoped count would report hundreds
	// of pages for the three-message chat above.
	other := createChatOfType(t, DB, owner, bot, "conversation")
	for i := 0; i < 200; i++ {
		createMessageForChatsTest(t, DB, other, owner, bot, "other")
	}

	first := listMessagesPage(t, DB, owner, chat.UUID, 2, 1)
	if first.TotalPages != 2 {
		t.Fatalf("expected 2 total_pages for a 3-message chat with limit=2, got %d (ci#825)", first.TotalPages)
	}
	if len(first.Rows) != 2 {
		t.Fatalf("expected 2 rows on page 1, got %d", len(first.Rows))
	}

	last := listMessagesPage(t, DB, owner, chat.UUID, 2, 2)
	if last.TotalPages != 2 {
		t.Fatalf("expected 2 total_pages on the partial last page, got %d", last.TotalPages)
	}
	if len(last.Rows) != 1 {
		t.Fatalf("expected the last page to be partial (1 row), got %d", len(last.Rows))
	}

	past := listMessagesPage(t, DB, owner, chat.UUID, 2, 3)
	if past.TotalPages != 2 {
		t.Fatalf("expected total_pages to stay stable past the end, got %d", past.TotalPages)
	}
	if len(past.Rows) != 0 {
		t.Fatalf("expected no rows past the end, got %d", len(past.Rows))
	}

	// The count must be scoped, not replaced: a 20-message chat still reports
	// 10 pages at limit=2.
	big := createChatOfType(t, DB, owner, bot, "conversation")
	for i := 0; i < 20; i++ {
		createMessageForChatsTest(t, DB, big, owner, bot, "big")
	}
	bigFirst := listMessagesPage(t, DB, owner, big.UUID, 2, 1)
	if bigFirst.TotalPages != 10 {
		t.Fatalf("expected 10 total_pages for a 20-message chat with limit=2, got %d", bigFirst.TotalPages)
	}
	if len(bigFirst.Rows) != 2 {
		t.Fatalf("expected 2 rows for the 20-message chat page 1, got %d", len(bigFirst.Rows))
	}
}

// TestListSharedInteractionMessagesTotalPagesIsChatScoped is the
// shared-interaction counterpart of the ci#825 guard.
func TestListSharedInteractionMessagesTotalPagesIsChatScoped(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "shared-paging-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "shared-paging-bot@example.com", false)

	chat := createChatOfType(t, DB, owner, bot, "interaction")
	for i := 0; i < 3; i++ {
		createMessageForChatsTest(t, DB, chat, owner, bot, "mine")
	}
	share := database.SharedChatInstance{
		ChatId:        chat.ID,
		OwningUserId:  owner.ID,
		ChatShareUUID: "share-paging-" + chat.UUID,
		ContentMode:   "unsecure",
	}
	if err := DB.Create(&share).Error; err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	other := createChatOfType(t, DB, owner, bot, "interaction")
	for i := 0; i < 200; i++ {
		createMessageForChatsTest(t, DB, other, owner, bot, "other")
	}

	first := listSharedMessagesPage(t, DB, share.ChatShareUUID, 2, 1)
	if first.TotalPages != 2 {
		t.Fatalf("expected 2 total_pages for a 3-message shared chat with limit=2, got %d (ci#825)", first.TotalPages)
	}
	if len(first.Rows) != 2 {
		t.Fatalf("expected 2 rows on shared page 1, got %d", len(first.Rows))
	}

	last := listSharedMessagesPage(t, DB, share.ChatShareUUID, 2, 2)
	if last.TotalPages != 2 {
		t.Fatalf("expected 2 total_pages on the partial shared last page, got %d", last.TotalPages)
	}
	if len(last.Rows) != 1 {
		t.Fatalf("expected the shared last page to be partial (1 row), got %d", len(last.Rows))
	}

	past := listSharedMessagesPage(t, DB, share.ChatShareUUID, 2, 3)
	if past.TotalPages != 2 {
		t.Fatalf("expected shared total_pages to stay stable past the end, got %d", past.TotalPages)
	}
	if len(past.Rows) != 0 {
		t.Fatalf("expected no shared rows past the end, got %d", len(past.Rows))
	}
}
