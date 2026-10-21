package chats

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"backend/database"
	"backend/runtimecfg"

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
	DB := database.SetupDatabase(cfg)
	closeChatsTestDB(t, DB)
	return DB
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
	return runBadgeRequestWith(t, DB, shareUUID, "", "")
}

func runBadgeRequestWith(t *testing.T, DB *gorm.DB, shareUUID, query, host string) *httptest.ResponseRecorder {
	t.Helper()
	h := &ChatsHandler{}
	target := "/api/interaction/" + shareUUID + "/badge.svg"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest("GET", target, nil)
	if host != "" {
		req.Host = host
	}
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

func TestBadgeShowsConfirmationRequiredState(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "bot@example.com")
	chat, share := createInteractionChatForBadge(t, DB, owner, botUser)

	finishBadgeInteraction(t, DB, chat, botUser, map[string]interface{}{
		"finished": true,
		"confirmable_actions": []map[string]interface{}{
			{"action_id": "a1", "status": "pending", "target_tool_name": "shell"},
		},
	})

	recorder := runBadgeRequest(t, DB, share.ChatShareUUID)
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, ">confirmation required</text>") {
		t.Fatalf("expected descriptive confirmation state text in badge, got: %s", body)
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

func TestBadgeDetailedIncludesHostAndRuntime(t *testing.T) {
	previous := runtimecfg.GetAll()
	runtimecfg.SetAll(map[string]runtimecfg.Value{})
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "bot@example.com")
	chat, share := createInteractionChatForBadge(t, DB, owner, botUser)

	finishBadgeInteraction(t, DB, chat, botUser, map[string]interface{}{"finished": true, "total_time": "1.5s"})

	recorder := runBadgeRequestWith(t, DB, share.ChatShareUUID, "", "badge.example.com")
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, "badge.example.com") {
		t.Fatalf("expected server host in detailed badge, got: %s", body)
	}
	if !strings.Contains(body, ">finished</text>") {
		t.Fatalf("expected finished state text in detailed badge, got: %s", body)
	}
	if !strings.Contains(body, ">00:01</text>") {
		t.Fatalf("expected formatted runtime from total_time metadata, got: %s", body)
	}
	if strings.Contains(body, "<animateTransform") {
		t.Fatalf("expected no ticker for finished badge, got: %s", body)
	}
	if cc := recorder.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=60") {
		t.Fatalf("expected terminal badge to be cacheable, got %q", cc)
	}
}

func TestBadgeSimpleVariantRendersLegacyBadge(t *testing.T) {
	previous := runtimecfg.GetAll()
	runtimecfg.SetAll(map[string]runtimecfg.Value{})
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	botUser := createBotUserForChatsTest(t, DB, "bot@example.com")
	chat, share := createInteractionChatForBadge(t, DB, owner, botUser)

	finishBadgeInteraction(t, DB, chat, botUser, map[string]interface{}{"finished": true, "total_time": "1.5s"})

	recorder := runBadgeRequestWith(t, DB, share.ChatShareUUID, "variant=simple", "badge.example.com")
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, ">finished</text>") {
		t.Fatalf("expected finished state text in simple badge, got: %s", body)
	}
	if strings.Contains(body, "badge.example.com") {
		t.Fatalf("expected simple badge to omit the host, got: %s", body)
	}
	if strings.Contains(body, "<animateTransform") {
		t.Fatalf("expected simple badge to omit the ticker, got: %s", body)
	}
	if strings.Contains(body, ">00:01</text>") {
		t.Fatalf("expected simple badge to omit the runtime, got: %s", body)
	}
}

func TestRenderBadgeSVGActiveUsesTicker(t *testing.T) {
	svg := string(renderBadgeSVG(badgeData{
		Label:   "open-chat",
		Host:    "badge.example.com",
		State:   "running",
		Color:   "#0969da",
		Runtime: &badgeRuntime{Seconds: 125, Active: true},
	}))

	for _, want := range []string{
		"badge.example.com",
		">running</text>",
		`<animateTransform`,
		`calcMode="discrete"`,
		`repeatCount="indefinite"`,
		`dur="6000s"`,
		`dur="600s"`,
		`dur="60s"`,
		`dur="10s"`,
		`begin="-125s"`,
		`begin="-5s"`,
		`transform="translate(0,-28)"`,
		`transform="translate(0,-70)"`,
	} {
		if !strings.Contains(svg, want) {
			t.Fatalf("expected %q in active ticker badge, got: %s", want, svg)
		}
	}
}

func TestRenderBadgeSVGStaticRuntimeIsFormattedText(t *testing.T) {
	svg := string(renderBadgeSVG(badgeData{
		Label:   "open-chat",
		Host:    "badge.example.com",
		State:   "finished",
		Color:   "#2ecc40",
		Runtime: &badgeRuntime{Seconds: 3725, Active: false},
	}))

	if strings.Contains(svg, "<animateTransform") {
		t.Fatalf("expected no ticker for static runtime, got: %s", svg)
	}
	if !strings.Contains(svg, ">1:02:05</text>") {
		t.Fatalf("expected H:MM:SS runtime text, got: %s", svg)
	}
}

func TestResolveBadgeHostPrefersPublicBaseURL(t *testing.T) {
	previous := runtimecfg.GetAll()
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"PUBLIC_BASE_URL": {Value: "https://ci.msgmate.io/"},
	})
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	req := httptest.NewRequest("GET", "/api/interaction/share/badge.svg", nil)
	req.Host = "ignored.example.com"

	if host := resolveBadgeHost(req); host != "ci.msgmate.io" {
		t.Fatalf("expected ci.msgmate.io, got %q", host)
	}
}

func TestResolveBadgeHostFallsBackToRequestHost(t *testing.T) {
	previous := runtimecfg.GetAll()
	runtimecfg.SetAll(map[string]runtimecfg.Value{})
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	req := httptest.NewRequest("GET", "/api/interaction/share/badge.svg", nil)
	req.Host = "badge.example.com"

	if host := resolveBadgeHost(req); host != "badge.example.com" {
		t.Fatalf("expected badge.example.com, got %q", host)
	}
}

func namedBotUserForChatsTest(t *testing.T, DB *gorm.DB, email, displayName string) *database.User {
	t.Helper()
	bot := createBotUserForChatsTest(t, DB, email)
	bot.Name = displayName
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to set bot display name: %v", err)
	}
	return bot
}

func TestBadgeRendersFollowUpRowFromRuntimeSelector(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	selectorBot := createBotUserForChatsTest(t, DB, "selector@example.com")
	implBot := namedBotUserForChatsTest(t, DB, "impl@example.com", "Implementation Agent")

	parent, share := createInteractionChatForBadge(t, DB, owner, selectorBot)
	followUp, followShare := createInteractionChatForBadge(t, DB, owner, implBot)

	meta := map[string]interface{}{
		"finished": true,
		"runtime_selectors": []interface{}{
			map[string]interface{}{
				"id":                      "sel-1",
				"status":                  RuntimeSelectorConfirmed,
				"started_chat_uuid":       followUp.UUID,
				"started_interaction_url": "/interaction/" + followShare.ChatShareUUID,
			},
		},
	}
	finishBadgeInteraction(t, DB, parent, selectorBot, meta)

	recorder := runBadgeRequest(t, DB, share.ChatShareUUID)
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	for _, want := range []string{
		">follow-up</text>",
		">Implementation Agent</text>",
		`href="/interaction/` + followShare.ChatShareUUID + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in follow-up badge, got: %s", want, body)
		}
	}
	// Two stacked rows: 20 + 3 + 20.
	if !strings.Contains(body, `height="43"`) {
		t.Fatalf("expected a two-row badge height, got: %s", body)
	}
}

func TestBadgeRendersFollowUpRowFromSharedConfig(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	parentBot := createBotUserForChatsTest(t, DB, "parent@example.com")
	followBot := namedBotUserForChatsTest(t, DB, "follow@example.com", "Follow Agent")

	parent, share := createInteractionChatForBadge(t, DB, owner, parentBot)
	followUp, followShare := createInteractionChatForBadge(t, DB, owner, followBot)

	configJSON, _ := json.Marshal(map[string]interface{}{
		"follow_up": map[string]interface{}{
			"chat_uuid": followUp.UUID,
			"agent":     "Picked Agent",
		},
	})
	shared := database.SharedChatConfig{ChatId: parent.ID, ConfigData: configJSON}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("failed to create shared config: %v", err)
	}
	if err := DB.Model(parent).Update("shared_config_id", shared.ID).Error; err != nil {
		t.Fatalf("failed to link shared config: %v", err)
	}

	finishBadgeInteraction(t, DB, parent, parentBot, map[string]interface{}{"finished": true})

	recorder := runBadgeRequest(t, DB, share.ChatShareUUID)
	body := recorder.Body.String()
	if recorder.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, ">Picked Agent</text>") {
		t.Fatalf("expected the configured follow-up agent in the badge, got: %s", body)
	}
	// The share uuid is resolved from the follow-up chat, so the follow-up row
	// links to its public interaction.
	if !strings.Contains(body, `href="/interaction/`+followShare.ChatShareUUID+`"`) {
		t.Fatalf("expected the derived follow-up interaction link, got: %s", body)
	}
}

func TestBadgeSimpleVariantOmitsFollowUpRow(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	parentBot := createBotUserForChatsTest(t, DB, "parent@example.com")
	followBot := namedBotUserForChatsTest(t, DB, "follow@example.com", "Follow Agent")

	parent, share := createInteractionChatForBadge(t, DB, owner, parentBot)
	followUp, _ := createInteractionChatForBadge(t, DB, owner, followBot)

	configJSON, _ := json.Marshal(map[string]interface{}{
		"follow_up": map[string]interface{}{"chat_uuid": followUp.UUID},
	})
	shared := database.SharedChatConfig{ChatId: parent.ID, ConfigData: configJSON}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("failed to create shared config: %v", err)
	}
	if err := DB.Model(parent).Update("shared_config_id", shared.ID).Error; err != nil {
		t.Fatalf("failed to link shared config: %v", err)
	}
	finishBadgeInteraction(t, DB, parent, parentBot, map[string]interface{}{"finished": true})

	recorder := runBadgeRequestWith(t, DB, share.ChatShareUUID, "variant=simple", "badge.example.com")
	body := recorder.Body.String()
	if strings.Contains(body, ">follow-up</text>") {
		t.Fatalf("expected the simple badge to omit the follow-up row, got: %s", body)
	}
}

func TestResolveFollowUpReferencePrefersConfigOverSelector(t *testing.T) {
	DB := setupBadgeTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner@example.com", false)
	parentBot := createBotUserForChatsTest(t, DB, "parent@example.com")
	configFollow := createBotUserForChatsTest(t, DB, "config-follow@example.com")
	selectorFollow := createBotUserForChatsTest(t, DB, "selector-follow@example.com")

	parent, _ := createInteractionChatForBadge(t, DB, owner, parentBot)
	configChat, _ := createInteractionChatForBadge(t, DB, owner, configFollow)
	selectorChat, _ := createInteractionChatForBadge(t, DB, owner, selectorFollow)

	configJSON, _ := json.Marshal(map[string]interface{}{
		"follow_up": map[string]interface{}{"chat_uuid": configChat.UUID, "agent": "Configured"},
	})
	shared := database.SharedChatConfig{ChatId: parent.ID, ConfigData: configJSON}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("failed to create shared config: %v", err)
	}
	if err := DB.Model(parent).Update("shared_config_id", shared.ID).Error; err != nil {
		t.Fatalf("failed to link shared config: %v", err)
	}
	parent.SharedConfigId = &shared.ID
	meta := map[string]interface{}{
		"finished": true,
		"runtime_selectors": []interface{}{
			map[string]interface{}{"id": "sel-1", "status": RuntimeSelectorConfirmed, "started_chat_uuid": selectorChat.UUID},
		},
	}
	finishBadgeInteraction(t, DB, parent, parentBot, meta)

	ref, ok := resolveFollowUpReference(DB, *parent)
	if !ok {
		t.Fatal("expected a follow-up reference")
	}
	if ref.ChatUUID != configChat.UUID || ref.Agent != "Configured" {
		t.Fatalf("expected the shared-config follow-up to win, got %+v", ref)
	}
}
