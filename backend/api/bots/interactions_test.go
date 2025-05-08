package bots

import (
	"backend/database"
	"backend/workqueue"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

func createBotForInteractionTest(t *testing.T, DB *gorm.DB, owner *database.User, name string) BotDTO {
	t.Helper()

	body, err := json.Marshal(createBotRequestPayload(name))
	if err != nil {
		t.Fatalf("failed to marshal create bot payload: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/bots", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", owner)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.Create(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200 creating bot, got %d: %s", rr.Code, rr.Body.String())
	}

	var response struct {
		Bot BotDTO `json:"bot"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode bot create response: %v", err)
	}
	if response.Bot.UUID == "" {
		t.Fatalf("expected bot uuid in response")
	}

	return response.Bot
}

func setupAsynqTest(t *testing.T) (*asynq.Client, *asynq.Inspector, func()) {
	t.Helper()

	redisOpt := asynq.RedisClientOpt{Addr: "redis:6379"}
	client := asynq.NewClient(redisOpt)
	inspector := asynq.NewInspector(redisOpt)

	cleanup := func() {
		_ = client.Close()
		_ = inspector.Close()
	}

	return client, inspector, cleanup
}

func TestCreateInteractionCreatesChatWithoutAutoShare(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.interaction@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "interaction-bot-no-share")

	queueClient, queueInspector, cleanupQueue := setupAsynqTest(t)
	defer cleanupQueue()

	body, err := json.Marshal(CreateBotInteractionRequest{
		Message:   "hello interaction",
		AutoShare: false,
	})
	if err != nil {
		t.Fatalf("failed to marshal interaction request: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/bots/"+bot.UUID+"/interactions", bytes.NewReader(body))
	req.SetPathValue("identifier", bot.UUID)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", owner)
	ctx = context.WithValue(ctx, "asynq_client", queueClient)
	ctx = context.WithValue(ctx, "asynq_inspector", queueInspector)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.CreateInteraction(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response BotInteractionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode interaction response: %v", err)
	}
	if response.ChatUUID == "" {
		t.Fatalf("expected chat_uuid in interaction response")
	}
	if response.ChatShareUUID != "" || response.ChatShare != nil || response.SharedInteractionURL != "" {
		t.Fatalf("expected no share fields when auto_share=false, got %#v", response)
	}

	var chat database.Chat
	if err := DB.Where("uuid = ?", response.ChatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to be created: %v", err)
	}
	if chat.ChatType != "interaction" {
		t.Fatalf("expected chat_type interaction, got %q", chat.ChatType)
	}
	if chat.User1Id != owner.ID && chat.User2Id != owner.ID {
		t.Fatalf("expected interaction chat to include owner user %d", owner.ID)
	}
	if chat.LatestMessageId == nil {
		t.Fatalf("expected latest_message_id to be set")
	}

	var msg database.Message
	if err := DB.Where("id = ?", *chat.LatestMessageId).First(&msg).Error; err != nil {
		t.Fatalf("expected initial message row: %v", err)
	}
	if msg.Text == nil || *msg.Text != "hello interaction" {
		t.Fatalf("expected initial message text to match request")
	}

	var shareCount int64
	if err := DB.Model(&database.SharedChatInstance{}).Where("chat_id = ?", chat.ID).Count(&shareCount).Error; err != nil {
		t.Fatalf("failed counting shares: %v", err)
	}
	if shareCount != 0 {
		t.Fatalf("expected no shared_chat_instances for auto_share=false, got %d", shareCount)
	}

	if _, err := queueInspector.GetTaskInfo(workqueue.QueueDefault, workqueue.BotReplyTaskID(chat.UUID)); err != nil {
		t.Fatalf("expected bot reply task to be queued: %v", err)
	}
}

func TestCreateInteractionCreatesChatWithAutoShare(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.interaction.share@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "interaction-bot-share")

	queueClient, queueInspector, cleanupQueue := setupAsynqTest(t)
	defer cleanupQueue()

	body, err := json.Marshal(CreateBotInteractionRequest{
		Message:   "hello shared interaction",
		AutoShare: true,
	})
	if err != nil {
		t.Fatalf("failed to marshal interaction request: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/bots/"+bot.UUID+"/interactions", bytes.NewReader(body))
	req.SetPathValue("identifier", bot.UUID)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", owner)
	ctx = context.WithValue(ctx, "asynq_client", queueClient)
	ctx = context.WithValue(ctx, "asynq_inspector", queueInspector)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.CreateInteraction(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response BotInteractionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode interaction response: %v", err)
	}
	if response.ChatUUID == "" {
		t.Fatalf("expected chat_uuid in response")
	}
	if response.ChatShareUUID == "" {
		t.Fatalf("expected chat_share_uuid for auto_share=true")
	}
	if response.ChatShare == nil {
		t.Fatalf("expected chat_share object for auto_share=true")
	}
	if response.ChatShare.ChatUUID != response.ChatUUID || response.ChatShare.ChatShareUUID != response.ChatShareUUID {
		t.Fatalf("chat_share payload mismatch: %#v", response.ChatShare)
	}
	if !strings.Contains(response.SharedInteractionURL, "/interaction/"+response.ChatShareUUID) {
		t.Fatalf("expected shared_interaction_url to contain share uuid, got %q", response.SharedInteractionURL)
	}

	var chat database.Chat
	if err := DB.Where("uuid = ?", response.ChatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to be created: %v", err)
	}
	if chat.ChatType != "interaction" {
		t.Fatalf("expected chat_type interaction, got %q", chat.ChatType)
	}
	if chat.User1Id != owner.ID && chat.User2Id != owner.ID {
		t.Fatalf("expected interaction chat to include owner user %d", owner.ID)
	}

	var share database.SharedChatInstance
	if err := DB.Where("chat_id = ? AND owning_user_id = ?", chat.ID, owner.ID).First(&share).Error; err != nil {
		t.Fatalf("expected shared_chat_instance row: %v", err)
	}
	if share.ChatShareUUID != response.ChatShareUUID {
		t.Fatalf("expected db share uuid %q to match response %q", share.ChatShareUUID, response.ChatShareUUID)
	}

	if _, err := queueInspector.GetTaskInfo(workqueue.QueueDefault, workqueue.BotReplyTaskID(chat.UUID)); err != nil {
		t.Fatalf("expected bot reply task to be queued: %v", err)
	}
}
