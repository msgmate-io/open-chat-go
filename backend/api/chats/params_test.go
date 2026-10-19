package chats

import (
	"net/http/httptest"
	"strings"
	"testing"
)

const validChatUUID = "6f1b0f9c-6c1e-4f5a-9f1c-0c8d5b2a7e31"

// TestParseChatUUID covers the shared {chat_uuid} path-parameter validation that
// replaced the four empty-string-only guards (GetChat, ListMessages,
// MessageSend, SignalSendMessage).
func TestParseChatUUID(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "canonical v4", raw: validChatUUID, want: validChatUUID},
		{name: "uppercase is normalized", raw: strings.ToUpper(validChatUUID), want: validChatUUID},
		{name: "dashless is normalized", raw: strings.ReplaceAll(validChatUUID, "-", ""), want: validChatUUID},
		{name: "urn form is normalized", raw: "urn:uuid:" + validChatUUID, want: validChatUUID},
		{name: "surrounding whitespace is trimmed", raw: "  " + validChatUUID + "\n", want: validChatUUID},
		{name: "missing", raw: "", wantErr: true},
		{name: "blank", raw: "   ", wantErr: true},
		{name: "plain garbage", raw: "not-a-uuid", wantErr: true},
		{name: "path traversal", raw: "..", wantErr: true},
		{name: "nul byte", raw: "6f1b0f9c-6c1e-4f5a-9f1c-0c8d5b2a7e31%00", wantErr: true},
		{name: "sql-ish payload", raw: "' OR 1=1 --", wantErr: true},
		{name: "too short", raw: validChatUUID[:20], wantErr: true},
		{name: "too long", raw: validChatUUID + "0", wantErr: true},
		{name: "oversized", raw: strings.Repeat("a", 4096), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/chats/x", nil)
			req.SetPathValue("chat_uuid", tc.raw)

			got, err := parseChatUUID(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %q", tc.raw, got)
				}
				if got != "" {
					t.Fatalf("expected an empty UUID alongside the error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestGetChatRejectsMalformedChatUUID guards that a non-empty but malformed
// {chat_uuid} is rejected before it reaches the database query.
func TestGetChatRejectsMalformedChatUUID(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "getchat-malformed@example.com", false)

	for _, raw := range []string{"", "not-a-uuid", "..", "' OR 1=1 --"} {
		req := httptest.NewRequest("GET", "/api/v1/chats/x", nil)
		req = withActionTasksUser(req, DB, owner)
		req.SetPathValue("chat_uuid", raw)
		rr := httptest.NewRecorder()

		h := &ChatsHandler{}
		h.GetChat(rr, req)

		if rr.Code != 400 {
			t.Fatalf("expected 400 for chat_uuid %q, got %d", raw, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "Invalid chat UUID") {
			t.Fatalf("expected an invalid-chat-UUID body for %q, got %q", raw, rr.Body.String())
		}
	}
}

// TestListMessagesRejectsMalformedChatUUID is the ListMessages counterpart of
// the guard above.
func TestListMessagesRejectsMalformedChatUUID(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "listmessages-malformed@example.com", false)

	for _, raw := range []string{"", "not-a-uuid", "..", "' OR 1=1 --"} {
		req := httptest.NewRequest("GET", "/api/v1/chats/x/messages", nil)
		req = withActionTasksUser(req, DB, owner)
		req.SetPathValue("chat_uuid", raw)
		rr := httptest.NewRecorder()

		h := &ChatsHandler{}
		h.ListMessages(rr, req)

		if rr.Code != 400 {
			t.Fatalf("expected 400 for chat_uuid %q, got %d", raw, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "Invalid chat UUID") {
			t.Fatalf("expected an invalid-chat-UUID body for %q, got %q", raw, rr.Body.String())
		}
	}
}

// TestGetChatAcceptsDashlessChatUUID guards that the normalization keeps
// equivalent spellings of a stored chat UUID resolvable.
func TestGetChatAcceptsDashlessChatUUID(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "getchat-dashless-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "getchat-dashless-bot@example.com", false)
	chat := createChatOfType(t, DB, owner, bot, "conversation")

	req := httptest.NewRequest("GET", "/api/v1/chats/x", nil)
	req = withActionTasksUser(req, DB, owner)
	req.SetPathValue("chat_uuid", strings.ToUpper(strings.ReplaceAll(chat.UUID, "-", "")))
	rr := httptest.NewRecorder()

	h := &ChatsHandler{}
	h.GetChat(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200 for a dashless upper-case chat UUID, got %d: %s", rr.Code, rr.Body.String())
	}
}
