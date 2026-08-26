package chats

import (
	"backend/database"
	"net/http"
	"strings"
)

func isInteractionChatType(chatType string) bool {
	return chatType == "interaction" || strings.HasPrefix(chatType, "interaction:")
}

// enforceBrowserTokenInteractionChat rejects restricted browser tokens for
// chats that are not interactions. Session and general-token callers are
// unaffected. Returns false (and writes the error response) when access must
// be denied.
func enforceBrowserTokenInteractionChat(w http.ResponseWriter, r *http.Request, chatType string) bool {
	if !database.IsBrowserToken(r.Context()) {
		return true
	}
	if isInteractionChatType(chatType) {
		return true
	}
	http.Error(w, "Browser tokens can only access interaction chats", http.StatusForbidden)
	return false
}
