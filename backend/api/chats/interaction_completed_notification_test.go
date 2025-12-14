package chats

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"backend/database"
	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func TestBuildInteractionCompletedBody(t *testing.T) {
	body := buildInteractionCompletedBody("https://ci.msgmate.io/interaction/abc", "assign", []string{"git", "repo:open-chat-go"})
	if !strings.HasPrefix(body, "Interaction completed: https://ci.msgmate.io/interaction/abc\n") {
		t.Fatalf("unexpected body prefix: %q", body)
	}
	if !strings.Contains(body, "Type: assign") || !strings.Contains(body, "Tags: git, repo:open-chat-go") {
		t.Fatalf("unexpected body: %q", body)
	}

	empty := buildInteractionCompletedBody("", "interaction", nil)
	if empty != "Interaction completed\nType: interaction" {
		t.Fatalf("unexpected empty-link body: %q", empty)
	}
}

func TestIsInteractionCompletionMetadata(t *testing.T) {
	finished := database.Message{DataType: "text", MetaData: database.JSONRaw(`{"finished":true}`)}
	if !isInteractionCompletionMetadata(finished, decodeOrFail(t, finished.MetaData)) {
		t.Fatalf("a finished, error-free text message must count as a completion")
	}

	failed := database.Message{MetaData: database.JSONRaw(`{"finished":true,"error":true}`)}
	if isInteractionCompletionMetadata(failed, decodeOrFail(t, failed.MetaData)) {
		t.Fatalf("an errored message must not count as a completion")
	}

	event := database.Message{DataType: "event", MetaData: database.JSONRaw(`{"finished":true,"event_type":"provider_retry"}`)}
	if isInteractionCompletionMetadata(event, decodeOrFail(t, event.MetaData)) {
		t.Fatalf("an in-flight event message must not count as a completion")
	}

	unfinished := database.Message{MetaData: database.JSONRaw(`{}`)}
	if isInteractionCompletionMetadata(unfinished, decodeOrFail(t, unfinished.MetaData)) {
		t.Fatalf("an unfinished message must not count as a completion")
	}
}

func TestInteractionCompletedTypeAndRepoContext(t *testing.T) {
	shared := &database.SharedChatConfig{ConfigData: json.RawMessage(`{"post_action":{"kind":"github_issue_comment","repo":"msgmate-io/open-chat-go","number":275,"issue_url":"https://github.com/msgmate-io/open-chat-go/issues/275"}}`)}
	chat := database.Chat{
		ChatType:     "interaction",
		Tags:         database.StringSliceJSON{"git", "via:assign", "repo:open-chat-go"},
		SharedConfig: shared,
	}
	if got := interactionCompletedType(chat); got != "assign" {
		t.Fatalf("interactionCompletedType = %q, want assign", got)
	}

	repo, number, subjectURL := interactionRepoContext(nil, chat)
	if repo != "msgmate-io/open-chat-go" || number != 275 || subjectURL == "" {
		t.Fatalf("interactionRepoContext = (%q, %d, %q)", repo, number, subjectURL)
	}

	noAction := database.Chat{ChatType: "interaction", Tags: database.StringSliceJSON{"repo:open-chat-go"}}
	repo, number, _ = interactionRepoContext(nil, noAction)
	if repo != "open-chat-go" || number != 0 {
		t.Fatalf("expected repo tag fallback, got (%q, %d)", repo, number)
	}
	if got := interactionCompletedType(noAction); got != "interaction" {
		t.Fatalf("interactionCompletedType without via tag = %q, want interaction", got)
	}
}

func TestMaybeDispatchInteractionCompletedNotification(t *testing.T) {
	previous := runtimecfg.GetAll()
	runtimecfg.SetAll(map[string]runtimecfg.Value{"PUBLIC_BASE_URL": {Value: "https://ci.msgmate.io/"}})
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	var captured map[string]interface{}
	if err := integrationinterface.Register(integrationinterface.Definition{
		Name: "notifications",
		Functions: map[string]integrationinterface.Function{
			"dispatch_notification": func(_ context.Context, payload map[string]interface{}) (interface{}, error) {
				captured = payload
				return map[string]interface{}{"ok": true}, nil
			},
		},
	}); err != nil {
		t.Fatalf("failed to register fake notifications integration: %v", err)
	}

	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "completed-owner@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "completed-bot@example.com")

	shared := database.SharedChatConfig{
		ChatId:     0,
		ConfigData: json.RawMessage(`{"post_action":{"kind":"github_issue_comment","repo":"msgmate-io/open-chat-go","number":275}}`),
	}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("failed to create shared config: %v", err)
	}

	chat := database.Chat{
		User1Id:        owner.ID,
		User2Id:        bot.ID,
		ChatType:       "interaction",
		Tags:           database.StringSliceJSON{"git", "via:assign"},
		SharedConfigId: &shared.ID,
		SharedConfig:   &shared,
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	share := database.SharedChatInstance{ChatId: chat.ID, OwningUserId: owner.ID, ChatShareUUID: "share-completed"}
	if err := DB.Create(&share).Error; err != nil {
		t.Fatalf("failed to create share instance: %v", err)
	}

	message := database.Message{
		ChatId:   chat.ID,
		SenderId: bot.ID,
		MetaData: database.JSONRaw(`{"finished":true}`),
	}
	maybeDispatchInteractionCompletedNotification(context.Background(), DB, chat, *bot, *owner, message)

	if captured == nil {
		t.Fatalf("expected the notifications dispatch to be called")
	}
	if captured["slug"] != notificationSlugInteractionCompleted {
		t.Fatalf("slug = %v, want %s", captured["slug"], notificationSlugInteractionCompleted)
	}
	if captured["receiver_user_uuid"] != owner.UUID {
		t.Fatalf("receiver_user_uuid = %v, want %s", captured["receiver_user_uuid"], owner.UUID)
	}
	body, _ := captured["body"].(string)
	if !strings.Contains(body, "https://ci.msgmate.io/interaction/share-completed") {
		t.Fatalf("body does not carry the interaction link: %q", body)
	}
	contextMap, _ := captured["context"].(map[string]interface{})
	if contextMap["repo"] != "msgmate-io/open-chat-go" {
		t.Fatalf("context repo = %v", contextMap["repo"])
	}

	// A pending action means the interaction is not actually complete yet.
	captured = nil
	pending := message
	pending.MetaData = database.JSONRaw(`{"finished":true,"confirmable_actions":[{"action_id":"a1","status":"pending"}]}`)
	maybeDispatchInteractionCompletedNotification(context.Background(), DB, chat, *bot, *owner, pending)
	if captured != nil {
		t.Fatalf("a pending confirmable action must not emit a completion notification")
	}
}

func decodeOrFail(t *testing.T, raw database.JSONRaw) map[string]interface{} {
	t.Helper()
	meta, ok := decodeMessageMetadata(raw)
	if !ok {
		t.Fatalf("failed to decode metadata %s", string(raw))
	}
	return meta
}
