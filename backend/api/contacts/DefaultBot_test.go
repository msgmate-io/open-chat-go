package contacts

import (
	"backend/api/websocket"
	"backend/database"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"
)

func getDefaultBot(t *testing.T, DB *gorm.DB, user *database.User, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contacts/default-bot"+query, nil)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	ctx = context.WithValue(ctx, "websocket", websocket.NewWebSocketHandler())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h := &ContactsHander{}
	h.GetDefaultBot(rec, req)
	return rec
}

// TestGetDefaultBotAcceptsIdentifier guards the TUI's `--bot <username>` path:
// the default-bot endpoint may resolve a specific automated user by username,
// display name or contact token instead of only the platform "bot" user.
func TestGetDefaultBotAcceptsIdentifier(t *testing.T) {
	DB := setupContactsTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-default-bot@contacts.example")
	owner.IsAdmin = true
	if err := DB.Save(owner).Error; err != nil {
		t.Fatalf("failed to make the owner an admin: %v", err)
	}
	bot := createContactsTestUser(t, DB, "coder-bot@contacts.example")
	bot.IsAutomated = true
	bot.Name = "coder-bot"
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark bot automated: %v", err)
	}

	for _, query := range []string{
		"?bot=" + bot.Username,
		"?username=" + bot.Username,
		"?bot=" + bot.Name,
		"?bot=" + bot.ContactToken,
	} {
		rec := getDefaultBot(t, DB, owner, query)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", query, rec.Code, rec.Body.String())
		}
		var contact ListedContact
		if err := json.Unmarshal(rec.Body.Bytes(), &contact); err != nil {
			t.Fatalf("%s: decode: %v", query, err)
		}
		if contact.UserUUID != bot.UUID || contact.ContactToken != bot.ContactToken {
			t.Fatalf("%s: unexpected contact %+v", query, contact)
		}
	}
}

func TestGetDefaultBotUnknownIdentifierIs404(t *testing.T) {
	DB := setupContactsTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-missing-bot@contacts.example")
	rec := getDefaultBot(t, DB, owner, "?bot=does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestGetDefaultBotPrivateBotNeedsAccess(t *testing.T) {
	DB := setupContactsTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-private-bot@contacts.example")
	bot := createContactsTestUser(t, DB, "private-bot@contacts.example")
	bot.IsAutomated = true
	bot.Name = "private-bot"
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark bot automated: %v", err)
	}
	rec := getDefaultBot(t, DB, owner, "?bot="+bot.Username)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a non-contact private bot must not resolve, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, err := owner.AddContact(DB, bot); err != nil {
		t.Fatalf("failed to add contact: %v", err)
	}
	rec = getDefaultBot(t, DB, owner, "?bot="+bot.Username)
	if rec.Code != http.StatusOK {
		t.Fatalf("a contact bot must resolve, got %d (%s)", rec.Code, rec.Body.String())
	}
}
