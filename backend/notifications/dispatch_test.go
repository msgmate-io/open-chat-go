package notifications

import (
	"context"
	"path/filepath"
	"testing"

	"backend/database"
	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

func newNotificationsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "notifications_test.db"),
		Debug:    false,
		ResetDB:  true,
	})
}

func TestMatchesCreditsExhausted(t *testing.T) {
	cases := map[string]bool{
		"HTTP 402 This request requires more credits, or fewer max_tokens": true,
		"you can only afford 3092 tokens":                                  true,
		"insufficient_quota":                                               true,
		"exceeded your current quota":                                      true,
		"HTTP 500 upstream error":                                          false,
		"":                                                                 false,
	}
	for detail, want := range cases {
		if got := matchesCreditsExhausted(detail); got != want {
			t.Errorf("matchesCreditsExhausted(%q) = %v, want %v", detail, got, want)
		}
	}
}

func TestTruncateDetail(t *testing.T) {
	if got := truncateDetail("  short  "); got != "short" {
		t.Fatalf("truncateDetail trimmed = %q", got)
	}
	long := make([]byte, 400)
	for i := range long {
		long[i] = 'x'
	}
	if got := truncateDetail(string(long)); len(got) > 304 {
		t.Fatalf("truncateDetail did not bound the length: %d", len(got))
	}
}

func TestMaybeDispatchInteractionFailurePicksSlugAndOwner(t *testing.T) {
	previous := runtimecfg.GetAll()
	runtimecfg.SetAll(map[string]runtimecfg.Value{"PUBLIC_BASE_URL": {Value: "https://ci.msgmate.io/"}})
	t.Cleanup(func() { runtimecfg.SetAll(previous) })

	captured := map[string]map[string]interface{}{}
	if err := integrationinterface.Register(integrationinterface.Definition{
		Name: "notifications",
		Functions: map[string]integrationinterface.Function{
			"dispatch_notification": func(_ context.Context, payload map[string]interface{}) (interface{}, error) {
				if slug, _ := payload["slug"].(string); slug != "" {
					captured[slug] = payload
				}
				return map[string]interface{}{"ok": true}, nil
			},
		},
	}); err != nil {
		t.Fatalf("failed to register fake notifications integration: %v", err)
	}

	DB := newNotificationsTestDB(t)
	owner := database.User{Name: "owner", Username: "owner", Email: "owner@example.com", IsAutomated: false}
	if err := DB.Create(&owner).Error; err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}
	bot := database.User{Name: "bot", Username: "bot", Email: "bot@example.com", IsAutomated: true}
	if err := DB.Create(&bot).Error; err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	MaybeDispatchInteractionFailure(context.Background(), DB, chat.UUID, "HTTP 402 requires more credits")
	dispatched := captured[SlugProviderCreditsExhausted]
	if dispatched == nil {
		t.Fatalf("expected provider_credits_exhausted dispatch")
	}
	if dispatched["receiver_user_uuid"] != owner.UUID {
		t.Fatalf("receiver_user_uuid = %v, want %s", dispatched["receiver_user_uuid"], owner.UUID)
	}
	contextMap, _ := dispatched["context"].(map[string]interface{})
	if contextMap["remedy"] == nil {
		t.Fatalf("credits dispatch should carry a remedy, got %v", contextMap)
	}

	MaybeDispatchInteractionFailure(context.Background(), DB, chat.UUID, "HTTP 500 upstream")
	if captured[SlugInteractionError] == nil {
		t.Fatalf("expected interaction_error dispatch")
	}

	// A pure bot-to-bot chat has no human owner and must be skipped.
	bot2 := database.User{Name: "bot2", Username: "bot2", Email: "bot2@example.com", IsAutomated: true}
	if err := DB.Create(&bot2).Error; err != nil {
		t.Fatalf("failed to create second bot: %v", err)
	}
	botChat := database.Chat{User1Id: bot.ID, User2Id: bot2.ID, ChatType: "interaction"}
	if err := DB.Create(&botChat).Error; err != nil {
		t.Fatalf("failed to create bot chat: %v", err)
	}
	delete(captured, SlugInteractionError)
	MaybeDispatchInteractionFailure(context.Background(), DB, botChat.UUID, "HTTP 500 upstream")
	if captured[SlugInteractionError] != nil {
		t.Fatalf("a bot-to-bot interaction must not dispatch an owner notification")
	}
}
