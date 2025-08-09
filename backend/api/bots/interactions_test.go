package bots

import (
	"backend/database"
	"backend/workqueue"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
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

func postInteractionForTest(t *testing.T, DB *gorm.DB, owner *database.User, bot BotDTO, reqBody CreateBotInteractionRequest, client *asynq.Client, inspector *asynq.Inspector) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("failed to marshal interaction request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/v1/bots/"+bot.UUID+"/interactions", bytes.NewReader(body))
	req.SetPathValue("identifier", bot.UUID)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", owner)
	ctx = context.WithValue(ctx, "asynq_client", client)
	ctx = context.WithValue(ctx, "asynq_inspector", inspector)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h := &BotsHandler{}
	h.CreateInteraction(rr, req)
	return rr
}

func TestCreateInteractionStoresAttachmentMetadataAndSharesWithBot(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.interaction.attachments@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "interaction-bot-attachments")

	uploaded := database.UploadedFile{
		FileID:   "file-attachment-1",
		FileName: "secret.txt",
		Size:     42,
		MIMEType: "text/plain",
		OwnerID:  owner.ID,
	}
	if err := DB.Create(&uploaded).Error; err != nil {
		t.Fatalf("failed to create uploaded file: %v", err)
	}

	queueClient, queueInspector, cleanupQueue := setupAsynqTest(t)
	defer cleanupQueue()

	rr := postInteractionForTest(t, DB, owner, bot, CreateBotInteractionRequest{
		Message: "please read the attached file",
		Attachments: []BotInteractionAttachment{
			{FileID: uploaded.FileID, DisplayName: "secret.txt"},
		},
	}, queueClient, queueInspector)
	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response BotInteractionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode interaction response: %v", err)
	}

	var chat database.Chat
	if err := DB.Where("uuid = ?", response.ChatUUID).First(&chat).Error; err != nil {
		t.Fatalf("expected interaction chat to be created: %v", err)
	}
	if chat.LatestMessageId == nil {
		t.Fatalf("expected latest_message_id to be set")
	}

	var msg database.Message
	if err := DB.Where("id = ?", *chat.LatestMessageId).First(&msg).Error; err != nil {
		t.Fatalf("expected initial message row: %v", err)
	}
	if len(msg.MetaData) == 0 {
		t.Fatalf("expected attachment metadata to be persisted")
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(msg.MetaData, &meta); err != nil {
		t.Fatalf("failed to decode message metadata: %v", err)
	}
	rawAttachments, ok := meta["attachments"].([]interface{})
	if !ok || len(rawAttachments) != 1 {
		t.Fatalf("expected one persisted attachment, got %#v", meta["attachments"])
	}
	attachment, ok := rawAttachments[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected attachment object, got %#v", rawAttachments[0])
	}
	if attachment["file_id"] != uploaded.FileID {
		t.Fatalf("expected file_id %q, got %#v", uploaded.FileID, attachment["file_id"])
	}
	if attachment["mime_type"] != "text/plain" {
		t.Fatalf("expected mime_type to be enriched, got %#v", attachment["mime_type"])
	}
	if attachment["file_name"] != "secret.txt" {
		t.Fatalf("expected file_name to be enriched, got %#v", attachment["file_name"])
	}

	// The bot user must be granted view access so the reply pipeline can fetch
	// the attachment via the file API.
	var botUser database.User
	if err := DB.Where("uuid = ?", bot.BotUserUUID).First(&botUser).Error; err != nil {
		t.Fatalf("failed to load bot user: %v", err)
	}
	var access database.FileAccess
	if err := DB.Where("user_id = ? AND uploaded_file_id = ?", botUser.ID, uploaded.ID).First(&access).Error; err != nil {
		t.Fatalf("expected file access row for bot user: %v", err)
	}
	if access.Permission != "view" {
		t.Fatalf("expected view permission, got %q", access.Permission)
	}
}

func TestCreateInteractionRejectsUnknownAndForeignAttachments(t *testing.T) {
	DB := setupBotsTestDB(t)
	owner := createUserForBotsTest(t, DB, "owner.interaction.att-errors@example.com", false)
	other := createUserForBotsTest(t, DB, "other.interaction.att-errors@example.com", false)
	bot := createBotForInteractionTest(t, DB, owner, "interaction-bot-att-errors")

	foreign := database.UploadedFile{
		FileID:   "file-foreign-1",
		FileName: "foreign.txt",
		Size:     7,
		MIMEType: "text/plain",
		OwnerID:  other.ID,
	}
	if err := DB.Create(&foreign).Error; err != nil {
		t.Fatalf("failed to create foreign uploaded file: %v", err)
	}

	queueClient, queueInspector, cleanupQueue := setupAsynqTest(t)
	defer cleanupQueue()

	unknownRR := postInteractionForTest(t, DB, owner, bot, CreateBotInteractionRequest{
		Message:     "unknown file",
		Attachments: []BotInteractionAttachment{{FileID: "does-not-exist"}},
	}, queueClient, queueInspector)
	if unknownRR.Code != 400 {
		t.Fatalf("expected 400 for unknown attachment, got %d: %s", unknownRR.Code, unknownRR.Body.String())
	}

	foreignRR := postInteractionForTest(t, DB, owner, bot, CreateBotInteractionRequest{
		Message:     "foreign file",
		Attachments: []BotInteractionAttachment{{FileID: foreign.FileID}},
	}, queueClient, queueInspector)
	if foreignRR.Code != 403 {
		t.Fatalf("expected 403 for foreign attachment, got %d: %s", foreignRR.Code, foreignRR.Body.String())
	}
}
