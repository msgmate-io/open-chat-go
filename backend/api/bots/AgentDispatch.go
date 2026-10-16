package bots

import (
	"backend/api/chats"
	"backend/chatstate"
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// This file implements the trusted "agent dispatch" surface: a stable internal
// API a coding/lead/admin agent (or the hive-mind integration acting for it) uses
// to start and manage other coding agents from inside Open-Chat, without the
// GitHub workflow round-trip.
//
// The five verbs mirror the workflow-equivalent options (task message, repo/issue
// tags, autonomous/trust, approve_confirmations, auto_continue + markers,
// workspace/sandbox) and the coder toolset's continue/stop lifecycle:
//
//	POST /api/v1/agents/dispatch
//	GET  /api/v1/agents/interactions
//	GET  /api/v1/agents/interactions/{chat_uuid}
//	POST /api/v1/agents/interactions/{chat_uuid}/continue
//	POST /api/v1/agents/interactions/{chat_uuid}/stop
//
// The acting user is the session principal, except when the caller is an
// automated bot (a coding/lead agent), in which case its owner is the acting
// user: the dispatched interaction belongs to the human the agent represents and
// appears in their chat list. No arbitrary impersonation is possible because the
// acting user can only ever be the caller or the caller's own owner.

// resolveActingUser maps a session principal to the user an agent-management call
// acts for. A human session acts for itself; an automated (bot) session acts for
// the owner of its bot runtime config, falling back to the bot itself when no
// owner is recorded.
func resolveActingUser(DB *gorm.DB, sessionUser *database.User) (*database.User, error) {
	if sessionUser == nil {
		return nil, errors.New("no authenticated user")
	}
	if !sessionUser.IsAutomated {
		return sessionUser, nil
	}
	var runtime database.BotRuntimeConfig
	err := DB.Preload("OwnerUser").Where("bot_user_id = ? AND is_active = ?", sessionUser.ID, true).First(&runtime).Error
	if err == nil && runtime.OwnerUser.ID != 0 {
		owner := runtime.OwnerUser
		return &owner, nil
	}
	return sessionUser, nil
}

// AgentDispatchRequest is the dispatch body. It carries the task plus the
// workflow-equivalent options so an agent can reproduce what the GitHub
// trigger-open-chat-coding-interaction workflow does.
type AgentDispatchRequest struct {
	Bot                  string                 `json:"bot"`
	Message              string                 `json:"message"`
	Tags                 []string               `json:"tags,omitempty"`
	Autonomous           bool                   `json:"autonomous,omitempty"`
	ApproveConfirmations bool                   `json:"approve_confirmations,omitempty"`
	AutoContinue         bool                   `json:"auto_continue,omitempty"`
	CompletionMarker     string                 `json:"completion_marker,omitempty"`
	HumanMarker          string                 `json:"human_marker,omitempty"`
	Workspace            string                 `json:"workspace,omitempty"`
	Sandbox              string                 `json:"sandbox,omitempty"`
	ConfigOverrides      map[string]interface{} `json:"config_overrides,omitempty"`
	ToolInit             map[string]interface{} `json:"tool_init,omitempty"`
	RequireConfirmation  *bool                  `json:"require_confirmation,omitempty"`
	AutoShare            bool                   `json:"auto_share,omitempty"`
}

// AgentInteractionView is the stable, agent-facing view of a managed interaction.
type AgentInteractionView struct {
	ChatUUID              string     `json:"chat_uuid"`
	ChatType              string     `json:"chat_type"`
	Bot                   string     `json:"bot,omitempty"`
	BotUUID               string     `json:"bot_uuid,omitempty"`
	ActingUserUUID        string     `json:"acting_user_uuid,omitempty"`
	ActingUserUsername    string     `json:"acting_user_username,omitempty"`
	Tags                  []string   `json:"tags,omitempty"`
	State                 string     `json:"state"`
	IsActive              bool       `json:"is_active"`
	LatestMessageUUID     string     `json:"latest_message_uuid,omitempty"`
	LatestMessageFinished *bool      `json:"latest_message_finished,omitempty"`
	LatestMessageAt       *time.Time `json:"latest_message_at,omitempty"`
	AutoContinue          bool       `json:"auto_continue,omitempty"`
}

// AgentDispatch starts an interaction with a named bot as the acting user,
// carrying the workflow-equivalent options. The options are recorded on the
// chat's shared config under `agent_dispatch` so a watcher (or the hive-mind
// dispatcher) can attach the auto-continuation loop at dispatch time.
func (h *BotsHandler) AgentDispatch(w http.ResponseWriter, r *http.Request) {
	DB, sessionUser, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	queueClient, clientErr := util.GetAsynqClient(r)
	queueInspector, inspectorErr := util.GetAsynqInspector(r)
	if clientErr != nil || inspectorErr != nil {
		http.Error(w, "Unable to access async queue", http.StatusInternalServerError)
		return
	}
	actingUser, err := resolveActingUser(DB, sessionUser)
	if err != nil {
		http.Error(w, "Unable to resolve acting user", http.StatusBadRequest)
		return
	}

	var req AgentDispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Bot) == "" {
		http.Error(w, "bot is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	runtime, err := resolveReadableBot(DB, actingUser, strings.TrimSpace(req.Bot))
	if err != nil {
		if errors.Is(err, errAmbiguousIdentifier) {
			http.Error(w, "ambiguous bot identifier", http.StatusConflict)
			return
		}
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	overrides := req.ConfigOverrides
	if overrides == nil {
		overrides = map[string]interface{}{}
	}
	if meta := agentDispatchMeta(req); len(meta) > 0 {
		overrides["agent_dispatch"] = meta
	}

	createReq := CreateBotInteractionRequest{
		Message:         req.Message,
		Tags:            req.Tags,
		ConfigOverrides: overrides,
		ToolInit:        req.ToolInit,
		AutoShare:       req.AutoShare,
	}
	if req.RequireConfirmation != nil {
		createReq.RequireConfirmation = *req.RequireConfirmation
	}

	response, err := createBotInteraction(DB, queueClient, queueInspector, actingUser, runtime, createReq, requestBaseURL(r))
	if err != nil {
		writeCreateInteractionError(w, err)
		return
	}

	view := AgentInteractionView{
		ChatUUID:           response.ChatUUID,
		ChatType:           "interaction",
		Bot:                botDisplayName(runtime),
		BotUUID:            runtime.UUID,
		ActingUserUUID:     actingUser.UUID,
		ActingUserUsername: actingUser.Username,
		Tags:               database.NormalizeChatTags(req.Tags),
		AutoContinue:       req.AutoContinue,
	}
	if loaded, loadErr := findActingInteraction(DB, actingUser, response.ChatUUID); loadErr == nil {
		view = agentInteractionView(DB, queueInspector, actingUser, loaded)
	}

	respondAgentJSON(w, http.StatusOK, map[string]interface{}{
		"success":                true,
		"requires_confirmation":  response.RequiresConfirmation,
		"chat_uuid":              response.ChatUUID,
		"bot":                    view.Bot,
		"bot_uuid":               view.BotUUID,
		"acting_user_uuid":       view.ActingUserUUID,
		"acting_user_username":   view.ActingUserUsername,
		"tags":                   view.Tags,
		"auto_continue":          req.AutoContinue,
		"shared_interaction_url": response.SharedInteractionURL,
	})
}

// AgentList lists the acting user's interaction chats (all agents it may manage),
// optionally filtered by tags and state.
func (h *BotsHandler) AgentList(w http.ResponseWriter, r *http.Request) {
	DB, sessionUser, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	actingUser, err := resolveActingUser(DB, sessionUser)
	if err != nil {
		http.Error(w, "Unable to resolve acting user", http.StatusBadRequest)
		return
	}
	inspector, _ := util.GetAsynqInspector(r)

	page, limit := 1, 40
	if value, convErr := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page"))); convErr == nil && value > 0 {
		page = value
	}
	if value, convErr := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit"))); convErr == nil && value > 0 && value <= 200 {
		limit = value
	}

	query := DB.Where("(user1_id = ? OR user2_id = ?)", actingUser.ID, actingUser.ID).
		Where("chat_type = ? OR chat_type LIKE ?", "interaction", "interaction:%")
	if tagsParam := strings.TrimSpace(r.URL.Query().Get("tags")); tagsParam != "" {
		for _, tag := range strings.Split(tagsParam, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			query = query.Where("CAST(tags AS TEXT) LIKE ?", "%\""+tag+"\"%")
		}
	}

	var rows []database.Chat
	if err := query.Preload("User1").Preload("User2").Preload("LatestMessage").
		Order("updated_at desc").
		Offset((page - 1) * limit).Limit(limit).
		Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list interactions", http.StatusInternalServerError)
		return
	}

	stateFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("state")))
	views := make([]AgentInteractionView, 0, len(rows))
	for _, chat := range rows {
		view := agentInteractionView(DB, inspector, actingUser, chat)
		if stateFilter != "" && strings.ToLower(view.State) != stateFilter {
			continue
		}
		views = append(views, view)
	}

	respondAgentJSON(w, http.StatusOK, map[string]interface{}{
		"page":  page,
		"limit": limit,
		"total": len(views),
		"rows":  views,
	})
}

// AgentGet returns one managed interaction the acting user owns.
func (h *BotsHandler) AgentGet(w http.ResponseWriter, r *http.Request) {
	DB, sessionUser, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	actingUser, err := resolveActingUser(DB, sessionUser)
	if err != nil {
		http.Error(w, "Unable to resolve acting user", http.StatusBadRequest)
		return
	}
	inspector, _ := util.GetAsynqInspector(r)

	chat, err := findActingInteraction(DB, actingUser, strings.TrimSpace(r.PathValue("chat_uuid")))
	if err != nil {
		http.Error(w, "Interaction not found", http.StatusNotFound)
		return
	}
	respondAgentJSON(w, http.StatusOK, map[string]interface{}{
		"row": agentInteractionView(DB, inspector, actingUser, chat),
	})
}

// AgentContinue sends a nudge/steering message to a managed interaction as the
// acting user and enqueues the bot reply, resuming a stalled agent.
func (h *BotsHandler) AgentContinue(w http.ResponseWriter, r *http.Request) {
	DB, sessionUser, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	queueClient, clientErr := util.GetAsynqClient(r)
	queueInspector, inspectorErr := util.GetAsynqInspector(r)
	if clientErr != nil || inspectorErr != nil {
		http.Error(w, "Unable to access async queue", http.StatusInternalServerError)
		return
	}
	actingUser, err := resolveActingUser(DB, sessionUser)
	if err != nil {
		http.Error(w, "Unable to resolve acting user", http.StatusBadRequest)
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	chat, err := findActingInteraction(DB, actingUser, strings.TrimSpace(r.PathValue("chat_uuid")))
	if err != nil {
		http.Error(w, "Interaction not found", http.StatusNotFound)
		return
	}
	bot := automatedParticipant(chat)
	if bot == nil {
		http.Error(w, "Interaction has no automated participant", http.StatusConflict)
		return
	}

	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   actingUser.ID,
		ReceiverId: bot.ID,
		Text:       &req.Message,
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		return tx.Model(&database.Chat{}).Where("id = ?", chat.ID).Update("latest_message_id", message.ID).Error
	}); err != nil {
		http.Error(w, "Failed to send message", http.StatusInternalServerError)
		return
	}

	if _, enqueueErr := workqueue.EnqueueBotReply(queueClient, queueInspector, workqueue.BotReplyPayload{
		ChatUUID:    chat.UUID,
		MessageUUID: message.UUID,
		BotUserID:   bot.ID,
	}); enqueueErr != nil {
		http.Error(w, "Failed to enqueue bot reply", http.StatusInternalServerError)
		return
	}

	respondAgentJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"chat_uuid":    chat.UUID,
		"message_uuid": message.UUID,
	})
}

// AgentStop stops a managed interaction authoritatively: it aborts any in-flight
// external backend generation, cancels the queued bot reply and voids any parked
// confirmation.
func (h *BotsHandler) AgentStop(w http.ResponseWriter, r *http.Request) {
	DB, sessionUser, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	actingUser, err := resolveActingUser(DB, sessionUser)
	if err != nil {
		http.Error(w, "Unable to resolve acting user", http.StatusBadRequest)
		return
	}

	chat, err := findActingInteraction(DB, actingUser, strings.TrimSpace(r.PathValue("chat_uuid")))
	if err != nil {
		http.Error(w, "Interaction not found", http.StatusNotFound)
		return
	}

	cancelled := 0
	if automatedParticipant(chat) != nil {
		if backend := chatBackendForChat(DB, chat); backend != "" {
			if handler, ok := chatstate.LookupBackendInterruptHandler(backend); ok {
				handler(chat.UUID)
			}
		}
		if inspector, inspectorErr := util.GetAsynqInspector(r); inspectorErr == nil {
			workqueue.CancelBotReplyTask(inspector, chat.UUID)
		}
		if count, cancelErr := chats.CancelPendingConfirmableActionsForChat(DB, chat.ID, actingUser.UUID); cancelErr == nil {
			cancelled = count
		}
	}

	respondAgentJSON(w, http.StatusOK, map[string]interface{}{
		"success":                 true,
		"chat_uuid":               chat.UUID,
		"cancelled_confirmations": cancelled,
	})
}

// agentInteractionView renders the stable agent-facing view of a chat.
func agentInteractionView(DB *gorm.DB, inspector *asynq.Inspector, actingUser *database.User, chat database.Chat) AgentInteractionView {
	view := AgentInteractionView{
		ChatUUID:           chat.UUID,
		ChatType:           chat.ChatType,
		ActingUserUUID:     actingUser.UUID,
		ActingUserUsername: actingUser.Username,
		Tags:               []string(chat.Tags),
	}
	if bot := automatedParticipant(chat); bot != nil {
		view.Bot = userDisplayName(*bot)
		view.BotUUID = bot.UUID
	}
	if chat.LatestMessage != nil {
		t := chat.LatestMessage.CreatedAt
		view.LatestMessageAt = &t
	} else if !chat.UpdatedAt.IsZero() {
		t := chat.UpdatedAt
		view.LatestMessageAt = &t
	}
	if status, statusErr := chats.ResolveInteractionStatus(DB, inspector, chat); statusErr == nil {
		view.State = status.State
		view.IsActive = status.IsActive
		view.LatestMessageUUID = status.LatestMessageUUID
		view.LatestMessageFinished = status.LatestMessageFinished
	} else {
		view.State = "idle"
	}
	if config := sharedConfigForChat(DB, chat); config != nil {
		if dispatch, ok := config["agent_dispatch"].(map[string]interface{}); ok {
			view.AutoContinue, _ = dispatch["auto_continue"].(bool)
		}
	}
	return view
}

// findActingInteraction loads an interaction chat the acting user participates in.
func findActingInteraction(DB *gorm.DB, actingUser *database.User, chatUUID string) (database.Chat, error) {
	chat := database.Chat{}
	if strings.TrimSpace(chatUUID) == "" {
		return chat, gorm.ErrRecordNotFound
	}
	err := DB.Preload("User1").Preload("User2").Preload("LatestMessage").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, actingUser.ID, actingUser.ID).
		Where("chat_type = ? OR chat_type LIKE ?", "interaction", "interaction:%").
		First(&chat).Error
	return chat, err
}

// automatedParticipant returns the bot participant of a chat, if any.
func automatedParticipant(chat database.Chat) *database.User {
	if chat.User1.IsAutomated {
		return &chat.User1
	}
	if chat.User2.IsAutomated {
		return &chat.User2
	}
	return nil
}

// chatBackendForChat resolves the external chat backend name from a chat's shared
// config, if one is set.
func chatBackendForChat(DB *gorm.DB, chat database.Chat) string {
	config := sharedConfigForChat(DB, chat)
	if config == nil {
		return ""
	}
	return chatstate.ChatBackendNameFromConfig(config)
}

// sharedConfigForChat decodes a chat's persisted shared config.
func sharedConfigForChat(DB *gorm.DB, chat database.Chat) map[string]interface{} {
	var data []byte
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		data = chat.SharedConfig.ConfigData
	} else if chat.SharedConfigId != nil && *chat.SharedConfigId != 0 {
		var shared database.SharedChatConfig
		if err := DB.First(&shared, *chat.SharedConfigId).Error; err != nil || len(shared.ConfigData) == 0 {
			return nil
		}
		data = shared.ConfigData
	}
	if len(data) == 0 {
		return nil
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil
	}
	return config
}

// agentDispatchMeta captures the workflow-equivalent dispatch options so they are
// recorded on the chat and can be attached by a dispatcher/watcher.
func agentDispatchMeta(req AgentDispatchRequest) map[string]interface{} {
	meta := map[string]interface{}{}
	if req.Autonomous {
		meta["autonomous"] = true
	}
	if req.ApproveConfirmations {
		meta["approve_confirmations"] = true
	}
	if req.AutoContinue {
		meta["auto_continue"] = true
	}
	if value := strings.TrimSpace(req.CompletionMarker); value != "" {
		meta["completion_marker"] = value
	}
	if value := strings.TrimSpace(req.HumanMarker); value != "" {
		meta["human_marker"] = value
	}
	if value := strings.TrimSpace(req.Workspace); value != "" {
		meta["workspace"] = value
	}
	if value := strings.TrimSpace(req.Sandbox); value != "" {
		meta["sandbox"] = value
	}
	return meta
}

func botDisplayName(runtime database.BotRuntimeConfig) string {
	if name := strings.TrimSpace(runtime.Name); name != "" {
		return name
	}
	return strings.TrimSpace(runtime.BotUser.Username)
}

func userDisplayName(user database.User) string {
	if name := strings.TrimSpace(user.Name); name != "" {
		return name
	}
	return strings.TrimSpace(user.Username)
}

func respondAgentJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
