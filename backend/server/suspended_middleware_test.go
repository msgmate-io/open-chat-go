package server

import (
	"backend/database"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"
)

func createServerAccountStatesTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`CREATE TABLE account_states (
		uuid text, created_at datetime, updated_at datetime, user_id integer primary key,
		signup_date datetime, is_email_verified boolean, suspended boolean
	)`).Error; err != nil {
		t.Fatalf("failed to create account_states table: %v", err)
	}
}

func seedServerAccountState(t *testing.T, db *gorm.DB, userID uint, suspended bool) {
	t.Helper()
	if err := db.Exec(
		"INSERT INTO account_states (user_id, suspended, is_email_verified) VALUES (?, ?, ?)",
		userID, suspended, true,
	).Error; err != nil {
		t.Fatalf("failed to seed account state: %v", err)
	}
}

func serveSessionAuthRequest(db *gorm.DB, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chats", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: token})
	//nolint:staticcheck // mirrors the server's string "db" context key convention
	req = req.WithContext(context.WithValue(req.Context(), "db", db))
	rr := httptest.NewRecorder()
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rr, req)
	return rr
}

func createSessionForUser(t *testing.T, db *gorm.DB, user *database.User, token string) {
	t.Helper()
	session := database.Session{
		Token:  token,
		Data:   []byte{},
		Expiry: time.Now().Add(time.Hour),
		UserId: user.ID,
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
}

func TestAuthMiddlewareBlocksSuspendedSession(t *testing.T) {
	db := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, db, "suspended-session-user")
	createServerAccountStatesTable(t, db)
	seedServerAccountState(t, db, user.ID, true)
	createSessionForUser(t, db, user, "suspended-session-token")

	rr := serveSessionAuthRequest(db, "suspended-session-token")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected suspended session blocked with 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAuthMiddlewareAllowsActiveSession(t *testing.T) {
	db := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, db, "active-session-user")
	createServerAccountStatesTable(t, db)
	seedServerAccountState(t, db, user.ID, false)
	createSessionForUser(t, db, user, "active-session-token")

	rr := serveSessionAuthRequest(db, "active-session-token")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected active session allowed, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAuthMiddlewareBlocksSuspendedBearerToken(t *testing.T) {
	db := setupMiddlewareTestDB(t)
	user := createMiddlewareTestUser(t, db, "suspended-bearer-user")
	createServerAccountStatesTable(t, db)
	seedServerAccountState(t, db, user.ID, true)
	_, raw := createAccessToken(t, db, user, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/chats", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	//nolint:staticcheck // mirrors the server's string "db" context key convention
	req = req.WithContext(context.WithValue(req.Context(), "db", db))
	rr := httptest.NewRecorder()
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected suspended bearer token blocked with 403, got %d: %s", rr.Code, rr.Body.String())
	}
}
