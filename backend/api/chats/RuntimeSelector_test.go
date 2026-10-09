package chats

import (
	"backend/database"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"
)

func seedRuntimeSelector(t *testing.T, DB *gorm.DB, owner *database.User) (database.Chat, database.Message, *database.User) {
	t.Helper()

	botUser := createUserForChatsTest(t, DB, "bot-runtime-selector@example.com", false)
	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: botUser.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	text := "Which runtime should handle this?"
	meta, err := json.Marshal(map[string]interface{}{
		"finished": true,
		"runtime_selectors": []interface{}{
			map[string]interface{}{
				"id":          "tc-1",
				"status":      "pending",
				"title":       "Choose runtime",
				"recommended": "opencode",
				"choices": []interface{}{
					map[string]interface{}{"id": "opencode", "label": "OpenCode", "bot": "opencode"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal selector meta: %v", err)
	}
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   botUser.ID,
		ReceiverId: owner.ID,
		DataType:   "text",
		Text:       &text,
		MetaData:   database.JSONRaw(meta),
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create selector message: %v", err)
	}
	if err := DB.Model(&chat).Update("latest_message_id", message.ID).Error; err != nil {
		t.Fatalf("failed to update latest message: %v", err)
	}
	return chat, message, botUser
}

func callResolveRuntimeSelector(t *testing.T, DB *gorm.DB, user *database.User, chat database.Chat, message database.Message, body interface{}) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest("POST", "/api/v1/chats/"+chat.UUID+"/messages/"+message.UUID+"/runtime-selector/resolve", reader)
	} else {
		req = httptest.NewRequest("POST", "/api/v1/chats/"+chat.UUID+"/messages/"+message.UUID+"/runtime-selector/resolve", nil)
	}
	req.SetPathValue("chat_uuid", chat.UUID)
	req.SetPathValue("message_uuid", message.UUID)
	//nolint:staticcheck // the "db"/"user" string keys are the fixed context keys read by util.GetDBAndUser
	ctx := context.WithValue(req.Context(), "db", DB)
	//nolint:staticcheck // see above
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	h.ResolveRuntimeSelector(rr, req)
	return rr
}

func loadRuntimeSelectors(t *testing.T, DB *gorm.DB, message database.Message) (map[string]interface{}, []map[string]interface{}) {
	t.Helper()

	var stored database.Message
	if err := DB.Where("id = ?", message.ID).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload message: %v", err)
	}
	meta := map[string]interface{}{}
	if err := json.Unmarshal(stored.MetaData, &meta); err != nil {
		t.Fatalf("failed to decode message meta: %v", err)
	}
	return meta, parseRuntimeSelectorsFromMeta(meta)
}

func TestResolveRuntimeSelectorStartedPersistsMeta(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-started@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	startedChat := createChatOfType(t, DB, owner, bot, "interaction")
	startedURL := "/chat/" + startedChat.UUID

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id":             "tc-1",
		"decision":                "started",
		"started_chat_uuid":       startedChat.UUID,
		"started_interaction_url": startedURL,
	})
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var response ResolveRuntimeSelectorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Success || response.Status != RuntimeSelectorConfirmed {
		t.Fatalf("unexpected resolve response: %#v", response)
	}

	meta, selectors := loadRuntimeSelectors(t, DB, message)
	if len(selectors) != 1 {
		t.Fatalf("expected one selector, got %d", len(selectors))
	}
	if selectors[0]["status"] != RuntimeSelectorConfirmed {
		t.Fatalf("expected confirmed status, got %#v", selectors[0]["status"])
	}
	if selectors[0]["started_chat_uuid"] != startedChat.UUID {
		t.Fatalf("expected started chat uuid to persist, got %#v", selectors[0]["started_chat_uuid"])
	}
	if selectors[0]["started_interaction_url"] != startedURL {
		t.Fatalf("expected started interaction url to persist, got %#v", selectors[0]["started_interaction_url"])
	}
	if selectors[0]["decided_by"] != owner.UUID {
		t.Fatalf("expected decided_by to be recorded, got %#v", selectors[0]["decided_by"])
	}
	// Unrelated meta keys must survive the update.
	if finished, _ := meta["finished"].(bool); !finished {
		t.Fatalf("expected unrelated meta key to be preserved")
	}

	// The selector must stop counting as a pending action.
	var stored database.Message
	if err := DB.Where("id = ?", message.ID).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload message: %v", err)
	}
	if actions := pendingActionsForMessage(stored); len(actions) != 0 {
		t.Fatalf("expected no pending actions after resolve, got %+v", actions)
	}
}

func TestResolveRuntimeSelectorCancelledPersistsMeta(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-cancelled@example.com", false)
	chat, message, _ := seedRuntimeSelector(t, DB, owner)

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "cancelled",
	})
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if len(selectors) != 1 || selectors[0]["status"] != RuntimeSelectorCancelled {
		t.Fatalf("expected cancelled status, got %+v", selectors)
	}

	var stored database.Message
	if err := DB.Where("id = ?", message.ID).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload message: %v", err)
	}
	if actions := pendingActionsForMessage(stored); len(actions) != 0 {
		t.Fatalf("expected no pending actions after cancel, got %+v", actions)
	}
}

func TestResolveRuntimeSelectorAlreadyResolvedConflicts(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-conflict@example.com", false)
	chat, message, _ := seedRuntimeSelector(t, DB, owner)

	first := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "cancelled",
	})
	if first.Code != 200 {
		t.Fatalf("expected first resolve to succeed, got %d: %s", first.Code, first.Body.String())
	}
	second := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "started",
	})
	if second.Code != 409 {
		t.Fatalf("expected 409 for already resolved selector, got %d: %s", second.Code, second.Body.String())
	}
}

func TestResolveRuntimeSelectorClaimThenRecord(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-claim@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	startedChat := createChatOfType(t, DB, owner, bot, "interaction")
	startedURL := "/interaction/claim-share"

	// Phase 1: claim the selector without an interaction yet. A competing
	// widget instance must not be able to claim it afterwards.
	claim := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "started",
		"claim_id":    "claim-a",
	})
	if claim.Code != 200 {
		t.Fatalf("expected claim to succeed, got %d: %s", claim.Code, claim.Body.String())
	}
	_, selectors := loadRuntimeSelectors(t, DB, message)
	if selectors[0]["status"] != RuntimeSelectorConfirmed {
		t.Fatalf("expected confirmed after claim, got %#v", selectors[0]["status"])
	}
	if selectors[0]["claim_id"] != "claim-a" {
		t.Fatalf("expected claim id to persist, got %#v", selectors[0]["claim_id"])
	}
	if _, ok := selectors[0]["started_chat_uuid"]; ok {
		t.Fatalf("expected no started chat recorded during claim, got %#v", selectors[0]["started_chat_uuid"])
	}

	competing := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "started",
		"claim_id":    "claim-b",
	})
	if competing.Code != 409 {
		t.Fatalf("expected competing claim to conflict, got %d: %s", competing.Code, competing.Body.String())
	}

	// Phase 2: the claim owner records the interaction identifiers.
	record := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id":             "tc-1",
		"decision":                "started",
		"claim_id":                "claim-a",
		"started_chat_uuid":       startedChat.UUID,
		"started_interaction_url": startedURL,
	})
	if record.Code != 200 {
		t.Fatalf("expected record to succeed, got %d: %s", record.Code, record.Body.String())
	}
	_, selectors = loadRuntimeSelectors(t, DB, message)
	if selectors[0]["started_chat_uuid"] != startedChat.UUID {
		t.Fatalf("expected started chat uuid to persist, got %#v", selectors[0]["started_chat_uuid"])
	}
	if selectors[0]["started_interaction_url"] != startedURL {
		t.Fatalf("expected started url to persist, got %#v", selectors[0]["started_interaction_url"])
	}

	// A competing claim after the record is still rejected.
	late := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "started",
		"claim_id":    "claim-b",
	})
	if late.Code != 409 {
		t.Fatalf("expected late competing claim to conflict, got %d: %s", late.Code, late.Body.String())
	}
}

func TestResolveRuntimeSelectorClaimIsIdempotentForOwner(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reclaim@example.com", false)
	chat, message, _ := seedRuntimeSelector(t, DB, owner)

	for i := 0; i < 2; i++ {
		rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
			"selector_id": "tc-1",
			"decision":    "started",
			"claim_id":    "claim-a",
		})
		if rr.Code != 200 {
			t.Fatalf("expected claim retry %d to succeed, got %d: %s", i, rr.Code, rr.Body.String())
		}
	}
}

func TestResolveRuntimeSelectorRejectsInvalidDecision(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-invalid@example.com", false)
	chat, message, _ := seedRuntimeSelector(t, DB, owner)

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "maybe",
	})
	if rr.Code != 400 {
		t.Fatalf("expected 400 for invalid decision, got %d: %s", rr.Code, rr.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if len(selectors) != 1 || selectors[0]["status"] != RuntimeSelectorPending {
		t.Fatalf("expected selector to stay pending, got %+v", selectors)
	}
}

func TestResolveRuntimeSelectorRejectsNonInteractionStartedChat(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-noninteraction@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	conversationChat := createChatOfType(t, DB, owner, bot, "conversation")

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id":       "tc-1",
		"decision":          "started",
		"started_chat_uuid": conversationChat.UUID,
	})
	if rr.Code != 409 {
		t.Fatalf("expected 409 for a non-interaction started chat, got %d: %s", rr.Code, rr.Body.String())
	}
}
