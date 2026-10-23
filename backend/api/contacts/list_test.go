package contacts

import (
	"backend/api/websocket"
	"backend/database"
	"backend/server/util"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupContactsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	DB := database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "contacts_list_test.db"),
		ResetDB:  true,
	})
	t.Cleanup(func() {
		sqlDB, err := DB.DB()
		if err != nil {
			return
		}
		_ = sqlDB.Close()
	})
	return DB
}

func createContactsTestUser(t *testing.T, DB *gorm.DB, username string) *database.User {
	t.Helper()
	err, user := util.CreateUser(DB, username, "Passw0rd!", false)
	if err != nil {
		t.Fatalf("failed to create user %q: %v", username, err)
	}
	return user
}

func listContacts(t *testing.T, DB *gorm.DB, user *database.User, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contacts/list"+query, nil)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	ctx = context.WithValue(ctx, "websocket", websocket.NewWebSocketHandler())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h := &ContactsHander{}
	h.List(rec, req)
	return rec
}

// A page past the end must not be a 404. The python client's get_bot fallback
// GETs /api/v1/contacts/list and turned the 404 into a hard RuntimeError
// ("get_bot fallback failed with status 404") whenever a bot has no contact row
// (ci#747). The handler must answer 200 with an empty rows list.
func TestListContactsOutOfRangePageReturnsEmptyPage(t *testing.T) {
	DB := setupContactsTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-out-of-range@contacts.example")
	partner := createContactsTestUser(t, DB, "partner-out-of-range@contacts.example")
	if _, err := owner.AddContact(DB, partner); err != nil {
		t.Fatalf("failed to add contact: %v", err)
	}

	rec := listContacts(t, DB, owner, "?page=50&limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a page past the end is not an error): %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedContacts
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Rows == nil {
		t.Fatalf("rows must serialise as an empty array, not null; body = %s", rec.Body.String())
	}
	if len(resp.Rows) != 0 {
		t.Fatalf("got %d rows on an out-of-range page, want 0", len(resp.Rows))
	}
	if resp.TotalPages != 1 {
		t.Fatalf("TotalPages = %d, want 1", resp.TotalPages)
	}
}

// The count behind TotalPages must run against the caller-scoped query. It used
// to run on the unscoped handle because the Where clauses were chained after
// Scopes, so one user's contacts inflated every other user's page count
// (ci#747).
func TestListContactsTotalPagesIsCallerScoped(t *testing.T) {
	DB := setupContactsTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-scoped@contacts.example")
	stranger := createContactsTestUser(t, DB, "stranger-scoped@contacts.example")

	// The owner gets a single contact, the stranger gets 25.
	partner := createContactsTestUser(t, DB, "partner-scoped@contacts.example")
	if _, err := owner.AddContact(DB, partner); err != nil {
		t.Fatalf("failed to add owner contact: %v", err)
	}
	for i := 0; i < 25; i++ {
		other := createContactsTestUser(t, DB, "stranger-contact-"+string(rune('a'+i))+"@contacts.example")
		if _, err := stranger.AddContact(DB, other); err != nil {
			t.Fatalf("failed to add stranger contact: %v", err)
		}
	}

	rec := listContacts(t, DB, owner, "?page=1&limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedContacts
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Rows) != 1 {
		t.Fatalf("got %d rows for the owner, want 1", len(resp.Rows))
	}
	if resp.TotalPages != 1 {
		t.Fatalf("TotalPages = %d, want 1 (the owner has one contact; the stranger's 25 must not leak in)", resp.TotalPages)
	}
}
