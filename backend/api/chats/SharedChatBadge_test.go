package chats

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

func setupBadgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "chats_badge_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createInteractionChatForBadge(t *testing.T, DB *gorm.DB, owner, bot *database.User) (*database.Chat, *database.SharedChatInstance) {
	t.Helper()
	chat := database.Chat{
		User1Id:  owner.ID,
		User2Id:  bot.ID,
		ChatType: "interaction",
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	share := database.SharedChatInstance{
		ChatId:        chat.ID,
		OwningUserId:  owner.ID,
		ChatShareUUID: "share-" + chat.UUID,
	}
	if err := DB.Create(&share).Error; err != nil {
		t.Fatalf("failed to create share: %v", err)
	}
	return &chat, &share
}

func createBotUserForChatsTest(t *testing.T, DB *gorm.DB, name string) *database.User {
	t.Helper()
	botUser := createUserForChatsTest(t, DB, name, false)
	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}
	return botUser
}

func runBadgeRequest(t *testing.T, DB *gorm.DB, shareUUID string) *httptest.ResponseRecorder {
	t.Helper()
	h := &ChatsHandler{}
	req := httptest.NewRequest("GET", "/api/interaction/"+shareUUID+"/badge.svg", nil)
	req.SetPathValue("chat_share_uuid", shareUUID)
	req = req.WithContext(context.WithValue(req.Context(), "db", DB))
	recorder := httptest.NewRecorder()
	h.GetSharedInteractionBadge(recorder, req)
	return recorder
}

func finishBadgeInteraction(t *testing.T, DB *gorm.DB, chat *database.Chat, bot *database.User, meta map[string]interface{}) {
	t.Helper()
	metaJSON, _ := json.Marshal(meta)
	text := "done"
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: chat.User1Id,
		DataType:   "text",
		Text:       &text,
		MetaData:   metaJSON,
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}
}

func TestBadgeShowsFinishedState(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "bot@example.com")
	chat, share := createInteractionChatForBadge(t, DB, owner, botUser)

	finishBadgeInteraction(t, DB, chat, botUser, map[string]interface{}{"finished": true})

	recorder := runBadgeRequest(t, DB, share.ChatShareUUID)
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
		t.Fatalf("expected image/svg+xml content type, got %q", ct)
	}
	if !strings.Contains(body, ">finished</text>") {
		t.Fatalf("expected finished state text in badge, got: %s", body)
	}
	if !strings.Contains(body, "<svg") || !strings.Contains(body, "</svg>") {
		t.Fatalf("expected valid SVG, got: %s", body)
	}
}

func TestBadgeShowsFailedState(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "bot@example.com")
	chat, share := createInteractionChatForBadge(t, DB, owner, botUser)

	finishBadgeInteraction(t, DB, chat, botUser, map[string]interface{}{"finished": true, "error": true})

	recorder := runBadgeRequest(t, DB, share.ChatShareUUID)
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, ">failed</text>") {
		t.Fatalf("expected failed state text in badge, got: %s", body)
	}
}

func TestBadgeShowsIdleStateWithoutMessages(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "bot@example.com")
	_, share := createInteractionChatForBadge(t, DB, owner, botUser)

	recorder := runBadgeRequest(t, DB, share.ChatShareUUID)
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, ">idle</text>") {
		t.Fatalf("expected idle state text in badge, got: %s", body)
	}
}

func TestBadgeUnknownShareRendersNotFoundSVG(t *testing.T) {
	DB := setupBadgeTestDB(t)

	recorder := runBadgeRequest(t, DB, "does-not-exist")
	body := recorder.Body.String()
	if recorder.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, ">not found</text>") {
		t.Fatalf("expected not found state text in badge, got: %s", body)
	}
}
