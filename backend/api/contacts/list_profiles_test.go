package contacts

import (
	"backend/api/websocket"
	"backend/database"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// setupContactsProfilesTestDB is the same harness as setupContactsTestDB so the
// two suites can evolve independently without sharing helpers.
func setupContactsProfilesTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	DB := database.SetupTestDatabase(t, database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "contacts_list_profiles_test.db"),
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

// The public-profile lookup used to ignore the query error and unmarshal the
// zero-value struct, so a partner without a profile row (the common case for a
// fresh user) serialised malformed profile data (ci#TODO). It must instead
// yield an empty profile object.
func TestListContactsPartnerWithoutProfileGetsEmptyProfile(t *testing.T) {
	DB := setupContactsProfilesTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-noprofile@contacts.example")
	partner := createContactsTestUser(t, DB, "partner-noprofile@contacts.example")
	if _, err := owner.AddContact(DB, partner); err != nil {
		t.Fatalf("failed to add contact: %v", err)
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
		t.Fatalf("got %d rows, want 1", len(resp.Rows))
	}
	if resp.Rows[0].ProfileData == nil {
		t.Fatalf("profile_data must be an empty object, got nil: %s", rec.Body.String())
	}
	if len(resp.Rows[0].ProfileData) != 0 {
		t.Fatalf("profile_data = %v, want empty object", resp.Rows[0].ProfileData)
	}
}

// The profile lookup must surface stored profile data instead of the empty
// object the zero-value struct unmarshal used to produce.
func TestListContactsPartnerWithProfileGetsProfileData(t *testing.T) {
	DB := setupContactsProfilesTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-withprofile@contacts.example")
	partner := createContactsTestUser(t, DB, "partner-withprofile@contacts.example")
	if _, err := owner.AddContact(DB, partner); err != nil {
		t.Fatalf("failed to add contact: %v", err)
	}

	profile := database.PublicProfile{
		UserId:      partner.ID,
		ProfileData: json.RawMessage(`{"display_name":"Partner","theme":"dark"}`),
	}
	if err := DB.Create(&profile).Error; err != nil {
		t.Fatalf("failed to create public profile: %v", err)
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
		t.Fatalf("got %d rows, want 1", len(resp.Rows))
	}
	profileData := resp.Rows[0].ProfileData
	if profileData == nil {
		t.Fatalf("profile_data must not be nil: %s", rec.Body.String())
	}
	if got := profileData["display_name"]; got != "Partner" {
		t.Fatalf("display_name = %v, want %q", got, "Partner")
	}
	if got := profileData["theme"]; got != "dark" {
		t.Fatalf("theme = %v, want %q", got, "dark")
	}
}

// A corrupt profile row must not turn the contact list into a 500: the handler
// logs and serves an empty profile object.
func TestListContactsPartnerWithCorruptProfileDoesNotError(t *testing.T) {
	DB := setupContactsProfilesTestDB(t)
	owner := createContactsTestUser(t, DB, "owner-corruptprofile@contacts.example")
	partner := createContactsTestUser(t, DB, "partner-corruptprofile@contacts.example")
	if _, err := owner.AddContact(DB, partner); err != nil {
		t.Fatalf("failed to add contact: %v", err)
	}

	profile := database.PublicProfile{
		UserId:      partner.ID,
		ProfileData: json.RawMessage(`{"display_name":`),
	}
	if err := DB.Create(&profile).Error; err != nil {
		t.Fatalf("failed to create corrupt public profile: %v", err)
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
		t.Fatalf("got %d rows, want 1", len(resp.Rows))
	}
	if len(resp.Rows[0].ProfileData) != 0 {
		t.Fatalf("profile_data = %v, want empty object", resp.Rows[0].ProfileData)
	}
}

func listContactByToken(t *testing.T, DB *gorm.DB, user *database.User, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contacts/"+token, nil)
	req.SetPathValue("contact_token", token)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	ctx = context.WithValue(ctx, "websocket", websocket.NewWebSocketHandler())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h := &ContactsHander{}
	h.GetContactByToken(rec, req)
	return rec
}

// GetContactByToken had the same ignored-query bug: a user without a profile
// row must produce an empty profile object, not a zero-value-struct unmarshal.
func TestGetContactByTokenWithoutProfileGetsEmptyProfile(t *testing.T) {
	DB := setupContactsProfilesTestDB(t)
	owner := createContactsTestUser(t, DB, "ownertokennoprofile@contacts.example")
	partner := createContactsTestUser(t, DB, "partnertokennoprofile@contacts.example")
	if _, err := owner.AddContact(DB, partner); err != nil {
		t.Fatalf("failed to add contact: %v", err)
	}

	rec := listContactByToken(t, DB, owner, partner.ContactToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var listed ListedContact
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if listed.ProfileData == nil {
		t.Fatalf("profile_data must be an empty object, got nil: %s", rec.Body.String())
	}
	if len(listed.ProfileData) != 0 {
		t.Fatalf("profile_data = %v, want empty object", listed.ProfileData)
	}
}
