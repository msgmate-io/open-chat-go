package chats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

// ci#865: POST /bots/{identifier}/interactions stores a dispatched interaction
// owner-scoped — the participant is the *acting user*, ie the owner of the bot
// runtime — and the completion notification looks the interaction's share up
// under that owner id. The publish/unpublish routes keyed their share on the raw
// session principal (the bot user itself), so publishing a share for a freshly
// dispatched interaction created a share the notification could never find, and
// the generated CI client died parsing the plain-text 404 body the route
// returned ("JSONDecodeError: Expecting value: line 1 column 1").
func TestPublishKeysShareOnResolvedActingUserForBotSession(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ci865-pub-owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "ci865-pub-bot@example.com")
	stranger := createUserForChatsTest(t, DB, "ci865-pub-stranger@example.com", false)

	runtime := database.BotRuntimeConfig{
		Name:        "ci865-pub-bot",
		BotUserId:   botUser.ID,
		OwnerUserId: owner.ID,
		IsActive:    true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create bot runtime config: %v", err)
	}

	// The interaction a bot session just created: createBotInteraction stores
	// the acting user (the runtime owner) as one participant and the bot user as
	// the other.
	interaction := database.Chat{User1Id: owner.ID, User2Id: botUser.ID, ChatType: "interaction"}
	if err := DB.Create(&interaction).Error; err != nil {
		t.Fatalf("failed to create interaction chat: %v", err)
	}

	recorder := runPublishRequest(t, DB, botUser, interaction.UUID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected publish as the bot session to succeed, got %d (%s)", recorder.Code, recorder.Body.String())
	}

	var response SharedChatPublishResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("publish response must be decodable JSON: %v (%q)", err, recorder.Body.String())
	}
	if response.ChatShareUUID == "" {
		t.Fatalf("publish response must carry a share UUID: %q", recorder.Body.String())
	}

	var share database.SharedChatInstance
	if err := DB.Where("chat_id = ?", interaction.ID).First(&share).Error; err != nil {
		t.Fatalf("failed to load the share: %v", err)
	}
	if share.OwningUserId != owner.ID {
		t.Fatalf("share must be owned by the resolved acting user %d, got %d", owner.ID, share.OwningUserId)
	}
	if share.OwningUserId == botUser.ID {
		t.Fatalf("share must not be keyed on the raw bot principal")
	}

	// Unpublish via the same automated session must remove that same share.
	unpublish := runUnpublishRequest(t, DB, botUser, interaction.UUID)
	if unpublish.Code != http.StatusOK {
		t.Fatalf("expected unpublish as the bot session to succeed, got %d (%s)", unpublish.Code, unpublish.Body.String())
	}
	var remaining int64
	if err := DB.Model(&database.SharedChatInstance{}).Where("chat_id = ?", interaction.ID).Count(&remaining).Error; err != nil {
		t.Fatalf("failed to count shares: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("unpublish must delete the acting user's share, %d row(s) remain", remaining)
	}
	if stranger.ID == share.OwningUserId {
		t.Fatalf("unexpected share owner")
	}
}

// A failure of the acting-user lookup — here: a bot session that does not
// participate in the chat and owns it nowhere — must be answered as decodable
// JSON, so the generated client surfaces the failure instead of dying in its
// JSON parser.
func TestPublishNotFoundAnswersJSON(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ci865-json-owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "ci865-json-bot@example.com")
	unrelated := createUserForChatsTest(t, DB, "ci865-json-other@example.com", false)
	stranger := createUserForChatsTest(t, DB, "ci865-json-stranger@example.com", false)

	foreign := database.Chat{User1Id: unrelated.ID, User2Id: stranger.ID, ChatType: "interaction"}
	if err := DB.Create(&foreign).Error; err != nil {
		t.Fatalf("failed to create foreign chat: %v", err)
	}

	recorder := runPublishRequest(t, DB, botUser, foreign.UUID)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign chat, got %d (%s)", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("error body must be JSON, got Content-Type %q", got)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body must be decodable JSON: %v (%q)", err, recorder.Body.String())
	}
	if body["error"] != "Chat not found" {
		t.Fatalf("unexpected error body: %q", recorder.Body.String())
	}

	// Same contract for the invalid-UUID path.
	invalid := runPublishRequest(t, DB, owner, "not-a-uuid")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid chat UUID, got %d (%s)", invalid.Code, invalid.Body.String())
	}
	if err := json.Unmarshal(invalid.Body.Bytes(), &body); err != nil {
		t.Fatalf("400 body must be decodable JSON: %v (%q)", err, invalid.Body.String())
	}
}

// A plain (non-automated) session must keep exactly its own ownership: the
// acting-user resolution may never hand a share to someone else.
func TestPublishKeepsOwnershipForRegularSession(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ci865-plain-owner@example.com", false)
	other := createUserForChatsTest(t, DB, "ci865-plain-other@example.com", false)

	chat := database.Chat{User1Id: owner.ID, User2Id: other.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	if recorder := runPublishRequest(t, DB, owner, chat.UUID); recorder.Code != http.StatusOK {
		t.Fatalf("expected publish to succeed, got %d (%s)", recorder.Code, recorder.Body.String())
	}

	var share database.SharedChatInstance
	if err := DB.Where("chat_id = ?", chat.ID).First(&share).Error; err != nil {
		t.Fatalf("failed to load the share: %v", err)
	}
	if share.OwningUserId != owner.ID {
		t.Fatalf("regular session must own its own share, got %d", share.OwningUserId)
	}
}

// A bot session without an active runtime config keeps the raw principal as its
// ownership: the resolution must be a no-op rather than widening ownership.
func TestPublishKeepsRawPrincipalWithoutActiveRuntime(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ci865-nocfg-owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "ci865-nocfg-bot@example.com")

	// is_active defaults to true in the schema and gorm omits zero-value
	// booleans on insert, so deactivate it explicitly.
	runtime := database.BotRuntimeConfig{
		Name:        "ci865-nocfg-bot",
		BotUserId:   botUser.ID,
		OwnerUserId: owner.ID,
		IsActive:    true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create runtime config: %v", err)
	}
	if err := DB.Model(&runtime).Update("is_active", false).Error; err != nil {
		t.Fatalf("failed to deactivate runtime config: %v", err)
	}

	chat := database.Chat{User1Id: botUser.ID, User2Id: owner.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	if recorder := runPublishRequest(t, DB, botUser, chat.UUID); recorder.Code != http.StatusOK {
		t.Fatalf("expected publish to succeed, got %d (%s)", recorder.Code, recorder.Body.String())
	}

	var share database.SharedChatInstance
	if err := DB.Where("chat_id = ?", chat.ID).First(&share).Error; err != nil {
		t.Fatalf("failed to load the share: %v", err)
	}
	if share.OwningUserId != botUser.ID {
		t.Fatalf("without an active runtime the raw principal stays the owner, got %d", share.OwningUserId)
	}
}

func runPublishRequest(t *testing.T, DB *gorm.DB, user *database.User, chatUUID string) *httptest.ResponseRecorder {
	t.Helper()
	return runSharedChatRequest(t, DB, user, chatUUID, (*ChatsHandler).Publish)
}

func runUnpublishRequest(t *testing.T, DB *gorm.DB, user *database.User, chatUUID string) *httptest.ResponseRecorder {
	t.Helper()
	return runSharedChatRequest(t, DB, user, chatUUID, (*ChatsHandler).Unpublish)
}

func runSharedChatRequest(
	t *testing.T,
	DB *gorm.DB,
	user *database.User,
	chatUUID string,
	handler func(*ChatsHandler, http.ResponseWriter, *http.Request),
) *httptest.ResponseRecorder {
	t.Helper()
	h := &ChatsHandler{}
	req := httptest.NewRequest("POST", "/api/chat/"+chatUUID+"/publish", nil)
	req.SetPathValue("chat_uuid", chatUUID)
	req = req.WithContext(context.WithValue(req.Context(), "db", DB))
	req = req.WithContext(context.WithValue(req.Context(), "user", user))
	recorder := httptest.NewRecorder()
	handler(h, recorder, req)
	return recorder
}
