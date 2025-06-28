package chats

import (
	"backend/database"
	"backend/server/util"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

func setupBrowserAccessTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "chats_browser_access_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func createBrowserAccessUser(t *testing.T, DB *gorm.DB, name string) *database.User {
	t.Helper()
	err, user := util.CreateUser(DB, name, "Passw0rd!", false)
	if err != nil {
		t.Fatalf("failed to create user %q: %v", name, err)
	}
	return user
}

func createChatOfType(t *testing.T, DB *gorm.DB, user *database.User, partner *database.User, chatType string) database.Chat {
	t.Helper()
	chat := database.Chat{
		User1Id:  user.ID,
		User2Id:  partner.ID,
		ChatType: chatType,
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	return chat
}

func browserTokenContext(req *http.Request, DB *gorm.DB, user *database.User) *http.Request {
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	ctx = database.ContextWithAccessToken(ctx, &database.AccessToken{
		Audience: database.AudienceBrowserAPI,
		Scopes:   database.JoinTokenScopes([]string{database.ScopeInteractionsList, database.ScopeInteractionsRead}),
	})
	return req.WithContext(ctx)
}

func TestBrowserTokenGetChatRestrictedToInteractions(t *testing.T) {
	DB := setupBrowserAccessTestDB(t)
	user := createBrowserAccessUser(t, DB, "owner-access@example.com")
	partner := createBrowserAccessUser(t, DB, "partner-access@example.com")

	interactionChat := createChatOfType(t, DB, user, partner, "interaction")
	conversationChat := createChatOfType(t, DB, user, partner, "conversation")
	namespacedChat := createChatOfType(t, DB, user, partner, "integration:foo")

	h := &ChatsHandler{}

	t.Run("interaction chat allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/"+interactionChat.UUID, nil)
		req.SetPathValue("chat_uuid", interactionChat.UUID)
		req = browserTokenContext(req, DB, user)
		rec := httptest.NewRecorder()
		h.GetChat(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200 for interaction chat, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("conversation chat denied", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/"+conversationChat.UUID, nil)
		req.SetPathValue("chat_uuid", conversationChat.UUID)
		req = browserTokenContext(req, DB, user)
		rec := httptest.NewRecorder()
		h.GetChat(rec, req)
		if rec.Code != 403 {
			t.Fatalf("expected 403 for conversation chat, got %d", rec.Code)
		}
	})

	t.Run("namespaced non-interaction chat denied", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/"+namespacedChat.UUID, nil)
		req.SetPathValue("chat_uuid", namespacedChat.UUID)
		req = browserTokenContext(req, DB, user)
		rec := httptest.NewRecorder()
		h.GetChat(rec, req)
		if rec.Code != 403 {
			t.Fatalf("expected 403 for integration-namespaced chat, got %d", rec.Code)
		}
	})
}

func TestBrowserTokenListMessagesRestrictedToInteractions(t *testing.T) {
	DB := setupBrowserAccessTestDB(t)
	user := createBrowserAccessUser(t, DB, "owner-msgs@example.com")
	partner := createBrowserAccessUser(t, DB, "partner-msgs@example.com")

	interactionChat := createChatOfType(t, DB, user, partner, "interaction")
	conversationChat := createChatOfType(t, DB, user, partner, "conversation")

	h := &ChatsHandler{}

	t.Run("interaction messages allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/"+interactionChat.UUID+"/messages/list", nil)
		req.SetPathValue("chat_uuid", interactionChat.UUID)
		req = browserTokenContext(req, DB, user)
		rec := httptest.NewRecorder()
		h.ListMessages(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200 for interaction messages, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("conversation messages denied", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/"+conversationChat.UUID+"/messages/list", nil)
		req.SetPathValue("chat_uuid", conversationChat.UUID)
		req = browserTokenContext(req, DB, user)
		rec := httptest.NewRecorder()
		h.ListMessages(rec, req)
		if rec.Code != 403 {
			t.Fatalf("expected 403 for conversation messages, got %d", rec.Code)
		}
	})
}

func TestBrowserTokenListOnlyInteractions(t *testing.T) {
	DB := setupBrowserAccessTestDB(t)
	user := createBrowserAccessUser(t, DB, "owner-list@example.com")
	partner := createBrowserAccessUser(t, DB, "partner-list@example.com")

	createChatOfType(t, DB, user, partner, "interaction")
	createChatOfType(t, DB, user, partner, "conversation")

	h := &ChatsHandler{}

	t.Run("list returns only interaction chats", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/list", nil)
		req = browserTokenContext(req, DB, user)
		rec := httptest.NewRecorder()
		h.List(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var page ListedChatsPage
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("failed to decode list response: %v", err)
		}
		if len(page.Rows) != 1 {
			t.Fatalf("expected only 1 interaction chat, got %d", len(page.Rows))
		}
		if page.Rows[0].ChatType != "interaction" {
			t.Fatalf("expected interaction chat type, got %q", page.Rows[0].ChatType)
		}
	})

	t.Run("explicit non-interaction chat_types denied", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/list?chat_types=conversation", nil)
		req = browserTokenContext(req, DB, user)
		rec := httptest.NewRecorder()
		h.List(rec, req)
		if rec.Code != 403 {
			t.Fatalf("expected 403 for non-interaction chat_types, got %d", rec.Code)
		}
	})
}

func TestBrowserTokenInteractionStatusRestricted(t *testing.T) {
	DB := setupBrowserAccessTestDB(t)
	user := createBrowserAccessUser(t, DB, "owner-status@example.com")
	partner := createBrowserAccessUser(t, DB, "partner-status@example.com")

	interactionChat := createChatOfType(t, DB, user, partner, "interaction")
	conversationChat := createChatOfType(t, DB, user, partner, "conversation")

	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = inspector.Close() })

	h := &ChatsHandler{}
	addInspector := func(req *http.Request) *http.Request {
		ctx := req.Context()
		ctx = context.WithValue(ctx, "asynq_inspector", inspector)
		return req.WithContext(ctx)
	}

	t.Run("interaction status allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/"+interactionChat.UUID+"/status", nil)
		req.SetPathValue("chat_uuid", interactionChat.UUID)
		req = addInspector(browserTokenContext(req, DB, user))
		rec := httptest.NewRecorder()
		h.GetInteractionStatus(rec, req)
		if rec.Code != 200 {
			t.Fatalf("expected 200 for interaction status, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("conversation status denied", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/chats/"+conversationChat.UUID+"/status", nil)
		req.SetPathValue("chat_uuid", conversationChat.UUID)
		req = addInspector(browserTokenContext(req, DB, user))
		rec := httptest.NewRecorder()
		h.GetInteractionStatus(rec, req)
		if rec.Code != 403 {
			t.Fatalf("expected 403 for conversation status, got %d", rec.Code)
		}
	})
}

func TestSessionCallerUnaffectedByInteractionRestriction(t *testing.T) {
	DB := setupBrowserAccessTestDB(t)
	user := createBrowserAccessUser(t, DB, "owner-session@example.com")
	partner := createBrowserAccessUser(t, DB, "partner-session@example.com")

	conversationChat := createChatOfType(t, DB, user, partner, "conversation")

	h := &ChatsHandler{}
	req := httptest.NewRequest("GET", "/api/v1/chats/"+conversationChat.UUID, nil)
	req.SetPathValue("chat_uuid", conversationChat.UUID)
	ctx := context.WithValue(req.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", user)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.GetChat(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected session caller to read conversation chat, got %d: %s", rec.Code, rec.Body.String())
	}
}
