package database

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSlugifyUsername pins the slug contract used to derive a bot user's
// users.username (open-chat-go-ci#1158).
func TestSlugifyUsername(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Support Bot", "support-bot"},
		{"  My   Bot  ", "my-bot"},
		{"my-bot", "my-bot"},
		{"Explicit_Handle!", "explicit-handle"},
		{"---", ""},
		{"   ", ""},
		{"🤖🤖", ""},
		{"Bot 42", "bot-42"},
	}
	for _, tc := range cases {
		if got := SlugifyUsername(tc.in); got != tc.want {
			t.Errorf("SlugifyUsername(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEnsureUniqueUsernameNeverEmpty asserts the derivation always produces a
// non-empty, unique handle - an empty users.username is what made the second bot
// creation fail with a unique-constraint violation (open-chat-go-ci#1158).
func TestEnsureUniqueUsernameNeverEmpty(t *testing.T) {
	db := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "ensure_unique_username_test.db"),
		ResetDB:  true,
	})

	first, err := EnsureUniqueUsername(db, "Support Bot")
	if err != nil {
		t.Fatalf("EnsureUniqueUsername(first): %v", err)
	}
	if first != "support-bot" {
		t.Fatalf("expected first username %q, got %q", "support-bot", first)
	}
	// Persist it so the next call actually collides on the unique index.
	if err := db.Create(&User{Name: "Support Bot", Username: first, Email: "support-bot@bot.local"}).Error; err != nil {
		t.Fatalf("failed to persist first user: %v", err)
	}

	second, err := EnsureUniqueUsername(db, "support bot")
	if err != nil {
		t.Fatalf("EnsureUniqueUsername(second): %v", err)
	}
	if second == first {
		t.Fatalf("expected a distinct username on collision, both were %q", second)
	}
	if second != "support-bot-2" {
		t.Fatalf("expected deterministic suffix %q, got %q", "support-bot-2", second)
	}

	// An unslugifiable preferred value must fall back to a generated handle
	// instead of returning "".
	fallback, err := EnsureUniqueUsername(db, "🤖")
	if err != nil {
		t.Fatalf("EnsureUniqueUsername(fallback): %v", err)
	}
	if strings.TrimSpace(fallback) == "" {
		t.Fatalf("ci#1158: EnsureUniqueUsername returned an empty username")
	}
	if !strings.HasPrefix(fallback, "usr_") {
		t.Fatalf("expected generated fallback username with usr_ prefix, got %q", fallback)
	}
}
