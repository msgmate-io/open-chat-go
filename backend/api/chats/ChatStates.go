package chats

import (
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"net/http"
	"strings"
)

// chatStatesMaxChats caps the number of chats resolvable in a single batch
// request (one list page is ~40 rows).
const chatStatesMaxChats = 100

type ChatStateRow struct {
	ChatUUID string `json:"chat_uuid"`
	IsActive bool   `json:"is_active"`
	State    string `json:"state"`
	Source   string `json:"source"`
}

type ChatStatesResponse struct {
	States []ChatStateRow `json:"states"`
}

// GetChatStates batch-resolves the state of the given chats.
//
// The state is one of: idle, active (bot interaction running), finished
// (AI completion done), failed (error occurred), needs_confirmation (user
// confirmation required).
//
//	@Summary      Get chat states
//	@Description  Batch-resolve the current state of multiple owned chats
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuids query string true "Comma-separated chat UUIDs (max 100)"
//	@Success      200 {object} ChatStatesResponse "Chat states"
//	@Failure      400 {string} string "Invalid input"
//	@Failure      500 {string} string "Unable to resolve chat states"
//	@Router       /api/v1/chats/states [get]
func (h *ChatsHandler) GetChatStates(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		http.Error(w, "Async queue unavailable", http.StatusInternalServerError)
		return
	}

	chatUUIDs := parseChatStatesUUIDs(r.URL.Query().Get("chat_uuids"))
	if len(chatUUIDs) == 0 {
		http.Error(w, "chat_uuids query parameter is required", http.StatusBadRequest)
		return
	}

	browserToken := database.IsBrowserToken(r.Context())
	response := ChatStatesResponse{States: []ChatStateRow{}}
	// ci#850: one queue snapshot for the whole batch instead of a queue listing
	// per chat.
	queue := snapshotQueueActivity{snapshot: workqueue.SnapshotBotReplyActivity(inspector)}
	accessible := make([]database.Chat, 0, len(chatUUIDs))
	for _, chatUUID := range chatUUIDs {
		chat, err := findAccessibleChat(DB, user, chatUUID)
		if err != nil {
			continue
		}
		if browserToken && !isInteractionChatType(chat.ChatType) {
			continue
		}
		accessible = append(accessible, chat)
	}
	// One transcript-tail query for every chat of the poll rather than one per
	// chat (the poll resolves up to chatStatesMaxChats chats every few seconds).
	tails := loadChatMessageTails(DB, accessible)
	cachedStates := cachedChatStates(accessible)
	for _, chat := range accessible {
		if cached, ok := cachedStates[chat.UUID]; ok {
			response.States = append(response.States, cached)
			continue
		}
		status, err := resolveInteractionStatusWithQueue(DB, chat, queue, tails)
		if err != nil {
			continue
		}
		row := ChatStateRow{
			ChatUUID: status.ChatUUID,
			IsActive: status.IsActive,
			State:    status.State,
			Source:   status.Source,
		}
		storeChatState(row)
		response.States = append(response.States, row)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func parseChatStatesUUIDs(raw string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 16)
	for _, part := range strings.Split(raw, ",") {
		chatUUID := strings.TrimSpace(part)
		if chatUUID == "" {
			continue
		}
		if _, exists := seen[chatUUID]; exists {
			continue
		}
		seen[chatUUID] = struct{}{}
		out = append(out, chatUUID)
		if len(out) >= chatStatesMaxChats {
			break
		}
	}
	return out
}
