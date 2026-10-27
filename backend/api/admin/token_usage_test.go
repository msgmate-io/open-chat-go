package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

func setupTokenUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "token_usage_admin_test.db"),
		Debug:    false,
		ResetDB:  true,
	})
}

func TestGetTokenUsageOverviewAggregates(t *testing.T) {
	DB := setupTokenUsageTestDB(t)
	admin := database.User{Name: "admin", Username: "admin", Email: "admin@example.com", IsAdmin: true}
	bot := database.User{Name: "bot", Username: "bot", Email: "bot@example.com", IsAutomated: true}
	if err := DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&bot).Error; err != nil {
		t.Fatal(err)
	}
	chat := database.Chat{User1Id: admin.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatal(err)
	}
	messages := []database.Message{
		{ChatId: chat.ID, SenderId: bot.ID, MetaData: database.JSONRaw(`{"token_usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"cached_tokens":800}}}`)},
		{ChatId: chat.ID, SenderId: bot.ID, MetaData: database.JSONRaw(`{"token_usage":{"prompt_tokens":500,"completion_tokens":50,"total_tokens":550,"prompt_tokens_details":{"cached_tokens":0}}}`)},
	}
	for i := range messages {
		if err := DB.Create(&messages[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/token-usage?hours=24", nil)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", &admin)
	rec := httptest.NewRecorder()
	GetTokenUsageOverview(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp tokenUsageOverviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Totals.PromptTokens != 1500 || resp.Totals.CompletionTokens != 150 || resp.Totals.CachedTokens != 800 {
		t.Fatalf("unexpected totals: %+v", resp.Totals)
	}
	if resp.Totals.CacheHitRatio < 0.53 || resp.Totals.CacheHitRatio > 0.54 {
		t.Fatalf("cache hit ratio = %v, want ~0.533", resp.Totals.CacheHitRatio)
	}
	if len(resp.Rows) != 1 || resp.Rows[0].Bot != "bot" {
		t.Fatalf("unexpected rows: %+v", resp.Rows)
	}
	if resp.Rows[0].CacheHitRatio < 0.53 || resp.Rows[0].CacheHitRatio > 0.54 {
		t.Fatalf("per-row cache hit ratio = %v, want ~0.533", resp.Rows[0].CacheHitRatio)
	}
}

func TestGetTokenUsageOverviewForbidsNonAdmin(t *testing.T) {
	DB := setupTokenUsageTestDB(t)
	user := database.User{Name: "user", Username: "user", Email: "user@example.com", IsAdmin: false}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/token-usage", nil)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", &user)
	rec := httptest.NewRecorder()
	GetTokenUsageOverview(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rec.Code)
	}
}
