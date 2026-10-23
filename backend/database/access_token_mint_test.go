package database

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func setupAccessTokenMintTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "access_token_mint_test.db"),
		ResetDB:  true,
	})
	if err := db.AutoMigrate(&AccessToken{}); err != nil {
		t.Fatalf("failed to migrate access_tokens: %v", err)
	}
	return db
}

func TestMintScopedAccessToken(t *testing.T) {
	db := setupAccessTokenMintTestDB(t)

	raw, token, err := MintScopedAccessToken(db, 42, "coder-tui:abc", AudienceCoderTUI, "", time.Minute, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(raw, "ocat_") {
		t.Fatalf("expected an ocat_ prefixed raw token, got %q", raw)
	}
	if token.Audience != AudienceCoderTUI {
		t.Fatalf("expected audience %q, got %q", AudienceCoderTUI, token.Audience)
	}
	if token.ExpiresAt == nil || !token.ExpiresAt.After(time.Now()) {
		t.Fatalf("expected a future expiry, got %v", token.ExpiresAt)
	}
	if !token.IsRestricted() {
		t.Fatalf("expected a restricted (audience-bound) token")
	}
	if token.UserId != 42 {
		t.Fatalf("expected user id 42, got %d", token.UserId)
	}

	// Only the hash is persisted; the raw credential must never be stored.
	var stored AccessToken
	if err := db.First(&stored, token.ID).Error; err != nil {
		t.Fatalf("failed to reload token: %v", err)
	}
	if stored.TokenHash == raw || stored.TokenHash == "" {
		t.Fatalf("expected the token hash to be persisted, raw not stored")
	}

	// Wrong-path error handling.
	if _, _, err := MintScopedAccessToken(db, 0, "x", AudienceCoderTUI, "", time.Minute, 0); err == nil {
		t.Fatalf("expected an error for a zero user id")
	}
	if _, _, err := MintScopedAccessToken(db, 7, "x", "  ", "", time.Minute, 0); err == nil {
		t.Fatalf("expected an error for an empty audience")
	}
	if _, _, err := MintScopedAccessToken(nil, 7, "x", AudienceCoderTUI, "", time.Minute, 0); err == nil {
		t.Fatalf("expected an error for a nil database handle")
	}
}
