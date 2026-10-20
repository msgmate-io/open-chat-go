package tools

import (
	"backend/database"
	"backend/server/util"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// The confirmable-action handlers are mounted at
// /api/v1/interactions/{chat_uuid}/messages/{message_uuid}/confirmable-actions/{action_id}
// and used to guard only against an empty chat_uuid. A malformed value therefore
// fell through to the `WHERE uuid = ?` lookup. These tests pin the guard added in
// util.ParsePathUUID: malformed input is rejected with the same status/body the
// empty-value guard already produced, and a well-formed value still reaches the
// database lookup.

func setupToolsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "tools_confirmable_uuid_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupTestDatabase(t, cfg)
}

func createUserForToolsTest(t *testing.T, DB *gorm.DB, name string) *database.User {
	t.Helper()
	err, user := util.CreateUser(DB, name, "Passw0rd!", false)
	if err != nil {
		t.Fatalf("failed to create user %q: %v", name, err)
	}
	return user
}

// confirmableActionRequest builds a request the way the router does: path
// parameters are set explicitly and the DB/user the handlers read through
// util.GetDBAndUser are injected into the request context.
func confirmableActionRequest(
	t *testing.T,
	DB *gorm.DB,
	user *database.User,
	method string,
	pathValues map[string]string,
	body string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/interactions", strings.NewReader(body))
	for key, value := range pathValues {
		req.SetPathValue(key, value)
	}
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	rr := httptest.NewRecorder()
	handler(rr, req.WithContext(ctx))
	return rr
}

func confirmableActionHandlers() []struct {
	name string
	fn   func(*ToolsHandler) http.HandlerFunc
} {
	return []struct {
		name string
		fn   func(*ToolsHandler) http.HandlerFunc
	}{
		{"RejectConfirmableAction", func(h *ToolsHandler) http.HandlerFunc { return h.RejectConfirmableAction }},
		{"ExecuteConfirmableAction", func(h *ToolsHandler) http.HandlerFunc { return h.ExecuteConfirmableAction }},
	}
}

func TestConfirmableActionHandlersRejectMalformedChatUUID(t *testing.T) {
	DB := setupToolsTestDB(t)
	user := createUserForToolsTest(t, DB, "confirmable.malformed@example.com")

	malformed := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"not a uuid", "not-a-uuid"},
		{"path fragment", "12345/67890"},
		{"truncated uuid", "0f8fad5b-d9cb-469f-a165-7086772895"},
		{"sql fragment", "' OR 1=1 --"},
	}

	for _, handler := range confirmableActionHandlers() {
		handler := handler
		for _, tc := range malformed {
			tc := tc
			t.Run(handler.name+"/"+tc.name, func(t *testing.T) {
				h := &ToolsHandler{}
				rr := confirmableActionRequest(t, DB, user, http.MethodPost, map[string]string{
					"chat_uuid":    tc.value,
					"message_uuid": "message-uuid",
					"action_id":    "action-id",
				}, `{"reason":"no"}`, handler.fn(h))

				if rr.Code != http.StatusBadRequest {
					t.Fatalf("expected status 400 for chat_uuid %q, got %d: %s", tc.value, rr.Code, rr.Body.String())
				}
				if !strings.Contains(rr.Body.String(), "Invalid confirmable action path") {
					t.Fatalf("expected the invalid-path body for chat_uuid %q, got %q", tc.value, rr.Body.String())
				}
			})
		}
	}
}

// A well-formed chat UUID must not be caught by the new guard: it has to reach
// the ownership-scoped lookup and fail there with the pre-existing 404, which is
// what an unknown/foreign chat produced before this change too.
func TestConfirmableActionHandlersPassWellFormedChatUUIDToLookup(t *testing.T) {
	DB := setupToolsTestDB(t)
	user := createUserForToolsTest(t, DB, "confirmable.wellformed@example.com")

	for _, handler := range confirmableActionHandlers() {
		handler := handler
		for _, spelling := range []struct {
			name  string
			value string
		}{
			{"canonical", uuid.NewString()},
			{"uppercase", strings.ToUpper(uuid.NewString())},
			{"surrounding whitespace", " " + uuid.NewString() + " "},
		} {
			spelling := spelling
			t.Run(handler.name+"/"+spelling.name, func(t *testing.T) {
				h := &ToolsHandler{}
				rr := confirmableActionRequest(t, DB, user, http.MethodPost, map[string]string{
					"chat_uuid":    spelling.value,
					"message_uuid": "message-uuid",
					"action_id":    "action-id",
				}, `{"reason":"no"}`, handler.fn(h))

				if rr.Code != http.StatusNotFound {
					t.Fatalf("expected status 404 for chat_uuid %q, got %d: %s", spelling.value, rr.Code, rr.Body.String())
				}
				if !strings.Contains(rr.Body.String(), "Chat not found or access denied") {
					t.Fatalf("expected the chat-lookup body for chat_uuid %q, got %q", spelling.value, rr.Body.String())
				}
			})
		}
	}
}
