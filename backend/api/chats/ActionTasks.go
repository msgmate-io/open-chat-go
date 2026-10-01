package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

// actionTasksMaxChats caps how many owned chats are scanned for pending action
// tasks in a single request.
const actionTasksMaxChats = 300

// Action task kinds surfaced by pendingActionsForMessage.
const (
	ActionTaskKindConfirmableAction       = "confirmable_action"
	ActionTaskKindOpencodePermission      = "opencode_permission"
	ActionTaskKindOpencodeNeedsAction     = "opencode_needs_action"
	ActionTaskKindInteractionConfirmation = "interaction_confirmation"
	ActionTaskKindToolConfirmation        = "tool_confirmation"
)

// ActionTaskAction is a single pending action found on a message that requires
// the user's attention.
type ActionTaskAction struct {
	Kind           string `json:"kind"`
	ActionId       string `json:"action_id,omitempty"`
	Title          string `json:"title,omitempty"`
	Description    string `json:"description,omitempty"`
	TargetToolName string `json:"target_tool_name,omitempty"`
	DangerLevel    string `json:"danger_level,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// ActionTaskPartner is the compact partner identity returned with a task.
type ActionTaskPartner struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	Username     string `json:"username,omitempty"`
	ContactToken string `json:"contact_token,omitempty"`
	IsAutomated  bool   `json:"is_automated"`
}

// ActionTaskRow is one chat/message currently waiting on the user.
type ActionTaskRow struct {
	TaskKey       string                 `json:"task_key"`
	ChatUUID      string                 `json:"chat_uuid"`
	ChatType      string                 `json:"chat_type"`
	Partner       ActionTaskPartner      `json:"partner"`
	MessageUUID   string                 `json:"message_uuid"`
	MessageText   string                 `json:"message_text"`
	MessageSendAt string                 `json:"message_send_at"`
	MessageMeta   map[string]interface{} `json:"message_meta"`
	ToolCalls     []interface{}          `json:"tool_calls"`
	Actions       []ActionTaskAction     `json:"actions"`
	Reason        string                 `json:"reason"`
	// AdminView marks rows returned through the admin-wide scope (the acting
	// admin is not a participant). ChatOwner identifies the user whose
	// interaction needs the action, so an admin can tell them apart.
	AdminView bool               `json:"admin_view,omitempty"`
	ChatOwner *ActionTaskPartner `json:"chat_owner,omitempty"`
}

// ActionTasksResponse is the payload of the action-tasks endpoint.
type ActionTasksResponse struct {
	Count int             `json:"count"`
	Rows  []ActionTaskRow `json:"rows"`
}

// GetActionTasks returns every owned chat that currently waits on a user action.
//
//	@Summary      Get pending action tasks
//	@Description  List owned chats/interactions that currently require a user action. Admins may pass scope=all to inspect pending actions across all users.
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        count_only query int false "Only return the count (1/0)" default(0)
//	@Param        scope query string false "Admin only: 'all' to list every user's pending actions" default()
//	@Success      200 {object} chats.ActionTasksResponse "Pending action tasks"
//	@Failure      400 {string} string "Unable to get database or user"
//	@Failure      500 {string} string "Unable to resolve action tasks"
//	@Router       /api/v1/chats/action-tasks [get]
func (h *ChatsHandler) GetActionTasks(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	countOnly := strings.TrimSpace(r.URL.Query().Get("count_only")) == "1"
	// The admin-wide scope is opt-in and hard-gated on the admin flag; regular
	// users always stay scoped to their own chats.
	adminScope := user.IsAdmin && strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "all")

	var query *gorm.DB
	if adminScope {
		query = DB.Model(&database.Chat{})
	} else {
		query = DB.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID)
	}
	if database.IsBrowserToken(r.Context()) {
		query = query.Where("chat_type = ? OR chat_type LIKE ?", "interaction", "interaction:%")
	}

	var chats []database.Chat
	if err := query.Preload("User1").Preload("User2").
		Order("updated_at DESC").
		Limit(actionTasksMaxChats).
		Find(&chats).Error; err != nil {
		http.Error(w, "Unable to load chats", http.StatusInternalServerError)
		return
	}

	// Dismissals are per-user; the admin-wide scope shows the real pending state
	// and is not affected by the admin's own dismissals.
	dismissed := map[string]struct{}{}
	if !adminScope {
		dismissed, err = loadDismissedActionTaskKeys(DB, user.ID)
		if err != nil {
			http.Error(w, "Unable to load dismissals", http.StatusInternalServerError)
			return
		}
	}

	response := ActionTasksResponse{Count: 0, Rows: []ActionTaskRow{}}
	for _, chat := range chats {
		message, actions, ok := latestPendingActionMessage(DB, chat.ID)
		if !ok {
			continue
		}
		taskKey := actionTaskKey(chat.UUID, message.UUID)
		if _, skip := dismissed[taskKey]; skip {
			continue
		}
		response.Count++
		if !countOnly {
			response.Rows = append(response.Rows, buildActionTaskRow(user, chat, message, actions, adminScope))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// DismissActionTask hides a pending action task for the current user.
//
//	@Summary      Dismiss an action task
//	@Description  Ignore / mark a pending action task as completed so it no longer surfaces.
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        payload body object true "Dismissal payload" SchemaExample({"message_uuid":"..."})
//	@Success      200 {object} map[string]interface{} "Dismissed"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Chat or message not found"
//	@Failure      500 {string} string "Unable to dismiss task"
//	@Router       /api/v1/chats/{chat_uuid}/action-tasks/dismiss [post]
func (h *ChatsHandler) DismissActionTask(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := strings.TrimSpace(r.PathValue("chat_uuid"))
	if chatUUID == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	var payload struct {
		MessageUUID string `json:"message_uuid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	messageUUID := strings.TrimSpace(payload.MessageUUID)
	if messageUUID == "" {
		http.Error(w, "message_uuid is required", http.StatusBadRequest)
		return
	}

	chat, err := findAccessibleChat(DB, user, chatUUID)
	if err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}
	if !enforceBrowserTokenInteractionChat(w, r, chat.ChatType) {
		return
	}

	var message database.Message
	if err := DB.Where("uuid = ? AND chat_id = ?", messageUUID, chat.ID).First(&message).Error; err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	taskKey := actionTaskKey(chat.UUID, message.UUID)
	dismissal := database.ActionTaskDismissal{UserId: user.ID, ChatId: chat.ID, TaskKey: taskKey}
	if err := DB.Where("user_id = ? AND task_key = ?", user.ID, taskKey).FirstOrCreate(&dismissal).Error; err != nil {
		http.Error(w, "Unable to dismiss task", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "task_key": taskKey})
}

func actionTaskKey(chatUUID string, messageUUID string) string {
	return strings.ToLower(strings.TrimSpace(chatUUID)) + ":" + strings.ToLower(strings.TrimSpace(messageUUID))
}

func loadDismissedActionTaskKeys(DB *gorm.DB, userID uint) (map[string]struct{}, error) {
	var dismissals []database.ActionTaskDismissal
	if err := DB.Where("user_id = ?", userID).Find(&dismissals).Error; err != nil {
		return nil, err
	}
	keys := make(map[string]struct{}, len(dismissals))
	for _, dismissal := range dismissals {
		keys[dismissal.TaskKey] = struct{}{}
	}
	return keys, nil
}

func latestPendingActionMessage(DB *gorm.DB, chatID uint) (database.Message, []ActionTaskAction, bool) {
	var messages []database.Message
	err := DB.Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Limit(confirmationScanLimit).
		Find(&messages).Error
	if err != nil {
		return database.Message{}, nil, false
	}
	for _, message := range messages {
		actions := pendingActionsForMessage(message)
		if len(actions) > 0 {
			return message, actions, true
		}
	}
	return database.Message{}, nil, false
}

func buildActionTaskRow(user *database.User, chat database.Chat, message database.Message, actions []ActionTaskAction, adminScope bool) ActionTaskRow {
	partner := chat.User1
	if chat.User1Id == user.ID {
		partner = chat.User2
	}

	text := ""
	if message.Text != nil {
		text = *message.Text
	}

	meta := map[string]interface{}{}
	if len(message.MetaData) > 0 {
		_ = json.Unmarshal(message.MetaData, &meta)
	}

	toolCalls := []interface{}{}
	if message.ToolCalls != nil {
		for _, rawToolCall := range *message.ToolCalls {
			toolCall := map[string]interface{}{}
			if json.Unmarshal(rawToolCall, &toolCall) == nil {
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}

	reason := ""
	if len(actions) > 0 {
		reason = actions[0].Reason
	}
	if reason == "" {
		reason = actions[0].Kind
	}

	row := ActionTaskRow{
		TaskKey:  actionTaskKey(chat.UUID, message.UUID),
		ChatUUID: chat.UUID,
		ChatType: chat.ChatType,
		Partner: ActionTaskPartner{
			UUID:         partner.UUID,
			Name:         partner.Name,
			Username:     partner.Username,
			ContactToken: partner.ContactToken,
			IsAutomated:  partner.IsAutomated,
		},
		MessageUUID:   message.UUID,
		MessageText:   text,
		MessageSendAt: message.CreatedAt.UTC().Format(time.RFC3339),
		MessageMeta:   meta,
		ToolCalls:     toolCalls,
		Actions:       actions,
		Reason:        reason,
	}

	if adminScope {
		if owner, ok := actionTaskHumanOwner(user, chat); ok {
			row.AdminView = true
			row.ChatOwner = &ActionTaskPartner{
				UUID:         owner.UUID,
				Name:         owner.Name,
				Username:     owner.Username,
				ContactToken: owner.ContactToken,
				IsAutomated:  owner.IsAutomated,
			}
		}
	}

	return row
}

// actionTaskHumanOwner picks the non-automated participant of a chat that the
// admin does not participate in, so the admin view can attribute a pending
// action to the user who owns the interaction.
func actionTaskHumanOwner(admin *database.User, chat database.Chat) (database.User, bool) {
	participants := []database.User{chat.User1, chat.User2}
	for _, participant := range participants {
		if participant.ID == admin.ID || participant.IsAutomated {
			continue
		}
		return participant, true
	}
	for _, participant := range participants {
		if participant.ID != admin.ID {
			return participant, true
		}
	}
	return database.User{}, false
}

func actionTaskStringField(source map[string]interface{}, key string) string {
	if source == nil {
		return ""
	}
	if value, ok := source[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func actionTaskFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
