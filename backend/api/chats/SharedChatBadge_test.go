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
