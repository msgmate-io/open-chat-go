package chats

import (
	"backend/database"
	"backend/workqueue"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

func setupConfirmationAsynqTest(t *testing.T) (*asynq.Client, *asynq.Inspector, func()) {
	t.Helper()

	redisAddr := strings.TrimSpace(os.Getenv("ASYNQ_REDIS_ADDR"))
	if redisAddr == "" {
		redisAddr = "redis:6379"
	}

	redisOpt := asynq.RedisClientOpt{Addr: redisAddr}
	client := asynq.NewClient(redisOpt)
	inspector := asynq.NewInspector(redisOpt)

	cleanup := func() {
		_ = client.Close()
		_ = inspector.Close()
	}

	return client, inspector, cleanup
}

func seedInteractionConfirmation(t *testing.T, DB *gorm.DB, owner *database.User) (database.Chat, database.Message, database.Message) {
	t.Helper()

	botUser := createUserForChatsTest(t, DB, "bot-confirmation@example.com", false)
	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: botUser.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	sourceText := "start coding"
	source := database.Message{
		ChatId:     chat.ID,
		SenderId:   owner.ID,
		ReceiverId: botUser.ID,
		DataType:   "text",
		Text:       &sourceText,
	}
	if err := DB.Create(&source).Error; err != nil {
		t.Fatalf("failed to create source message: %v", err)
	}

	confirmationText := "Start coding interaction?"
	meta, err := json.Marshal(map[string]interface{}{
		"finished": true,
		"interaction_confirmation": map[string]interface{}{
			"status":              "pending",
			"source_message_uuid": source.UUID,
			"title":               "Start coding interaction?",
			"description":         "Approve to start, reject to cancel.",
			"chat_uuid":           chat.UUID,
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal confirmation meta: %v", err)
	}
	confirmation := database.Message{
		ChatId:     chat.ID,
		SenderId:   botUser.ID,
		ReceiverId: owner.ID,
		DataType:   "text",
		Text:       &confirmationText,
		MetaData:   database.JSONRaw(meta),
	}
	if err := DB.Create(&confirmation).Error; err != nil {
		t.Fatalf("failed to create confirmation message: %v", err)
	}
	if err := DB.Model(&chat).Update("latest_message_id", confirmation.ID).Error; err != nil {
		t.Fatalf("failed to update latest message: %v", err)
	}

	return chat, source, confirmation
}

func callInteractionConfirmation(t *testing.T, DB *gorm.DB, owner *database.User, chat database.Chat, confirmation database.Message, client *asynq.Client, inspector *asynq.Inspector, action string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("POST", "/api/v1/chats/"+chat.UUID+"/messages/"+confirmation.UUID+"/interaction-confirmation/"+action, nil)
	req.SetPathValue("chat_uuid", chat.UUID)
	req.SetPathValue("message_uuid", confirmation.UUID)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", owner)
	ctx = context.WithValue(ctx, "asynq_client", client)
	ctx = context.WithValue(ctx, "asynq_inspector", inspector)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &ChatsHandler{}
	if action == "approve" {
		h.ApproveInteractionConfirmation(rr, req)
	} else {
		h.RejectInteractionConfirmation(rr, req)
	}
	return rr
}

func TestApproveInteractionConfirmationQueuesBotReply(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-approve@example.com", false)
	chat, source, confirmation := seedInteractionConfirmation(t, DB, owner)

	client, inspector, cleanup := setupConfirmationAsynqTest(t)
	defer cleanup()

	rr := callInteractionConfirmation(t, DB, owner, chat, confirmation, client, inspector, "approve")
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var response InteractionConfirmationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Success || response.Status != InteractionConfirmationApproved || !response.Queued {
		t.Fatalf("unexpected approve response: %#v", response)
	}

	var stored database.Message
	if err := DB.Where("id = ?", confirmation.ID).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload confirmation: %v", err)
	}
	meta, err := parseInteractionConfirmationMeta(stored)
	if err != nil {
		t.Fatalf("failed to parse confirmation meta: %v", err)
	}
	if meta.Status != InteractionConfirmationApproved {
		t.Fatalf("expected approved status, got %q", meta.Status)
	}
	if meta.SourceMessageUUID != source.UUID {
		t.Fatalf("expected source uuid to be preserved, got %q", meta.SourceMessageUUID)
	}

	task, err := inspector.GetTaskInfo(workqueue.QueueDefault, workqueue.BotReplyTaskID(chat.UUID))
	if err != nil || task == nil {
		t.Fatalf("expected bot reply task to be queued after approve: %v", err)
	}
}

func TestRejectInteractionConfirmationFailsInteraction(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-reject@example.com", false)
	chat, _, confirmation := seedInteractionConfirmation(t, DB, owner)

	client, inspector, cleanup := setupConfirmationAsynqTest(t)
	defer cleanup()

	rr := callInteractionConfirmation(t, DB, owner, chat, confirmation, client, inspector, "reject")
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var response InteractionConfirmationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !response.Success || response.Status != InteractionConfirmationRejected || response.Queued {
		t.Fatalf("unexpected reject response: %#v", response)
	}

	var stored database.Message
	if err := DB.Where("id = ?", confirmation.ID).First(&stored).Error; err != nil {
		t.Fatalf("failed to reload confirmation: %v", err)
	}
	meta, err := parseInteractionConfirmationMeta(stored)
	if err != nil {
		t.Fatalf("failed to parse confirmation meta: %v", err)
	}
	if meta.Status != InteractionConfirmationRejected {
		t.Fatalf("expected rejected status, got %q", meta.Status)
	}

	var reloadedChat database.Chat
	if err := DB.Where("id = ?", chat.ID).First(&reloadedChat).Error; err != nil {
		t.Fatalf("failed to reload chat: %v", err)
	}
	if reloadedChat.LatestMessageId == nil {
		t.Fatalf("expected latest message to be updated on reject")
	}
	var event database.Message
	if err := DB.Where("id = ?", *reloadedChat.LatestMessageId).First(&event).Error; err != nil {
		t.Fatalf("failed to load event message: %v", err)
	}
	if event.DataType != "event" {
		t.Fatalf("expected event message, got data_type %q", event.DataType)
	}
	var eventMeta map[string]interface{}
	if err := json.Unmarshal(event.MetaData, &eventMeta); err != nil {
		t.Fatalf("failed to decode event meta: %v", err)
	}
	if errFlag, _ := eventMeta["error"].(bool); !errFlag {
		t.Fatalf("expected rejected event to carry error=true")
	}
	if _, err := inspector.GetTaskInfo(workqueue.QueueDefault, workqueue.BotReplyTaskID(chat.UUID)); err == nil {
		t.Fatalf("expected no bot reply task to be queued after reject")
	}
}

func TestInteractionConfirmationAlreadyResolvedConflicts(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-conflict@example.com", false)
	chat, _, confirmation := seedInteractionConfirmation(t, DB, owner)

	client, inspector, cleanup := setupConfirmationAsynqTest(t)
	defer cleanup()

	first := callInteractionConfirmation(t, DB, owner, chat, confirmation, client, inspector, "approve")
	if first.Code != 200 {
		t.Fatalf("expected first approve to succeed, got %d: %s", first.Code, first.Body.String())
	}
	second := callInteractionConfirmation(t, DB, owner, chat, confirmation, client, inspector, "approve")
	if second.Code != 409 {
		t.Fatalf("expected 409 for already resolved confirmation, got %d: %s", second.Code, second.Body.String())
	}
}
