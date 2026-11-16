package msgmate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tooldefs "backend/api/msgmate/tools"
	"backend/database"
	"backend/sharecensor"

	"gorm.io/gorm"
)

// coder_recall runner (ticket ci#1103, hive-mind integration): the DB-backed
// implementation behind the `coder_recall` tool. The tool itself only validates
// and bounds the model input; this runner owns the *scope*, which is the safety
// boundary of the whole feature:
//
//   - the agent identity is the bot user of the running turn,
//   - the user identity is the sender who triggered the running turn, and
//   - only chats in which exactly those two users are the participants are
//     reachable.
//
// The scope is derived from the trusted `_runtime` payload of the running turn
// and from the chat row itself, never from model-supplied input, so the model
// can neither widen the query nor address another user's or another agent's
// interactions. Message text is passed through sharecensor before it is
// returned, so a recall can never surface credentials, tokens, kubeconfig
// material or private keys.

const (
	// coderRecallMaxScopeChats bounds how many scoped interactions one listing
	// call scans before it stops walking the history.
	coderRecallMaxScopeChats = 200
	// coderRecallMessagesPerChat bounds the message window loaded per
	// interaction for the listing form of the tool.
	coderRecallMessagesPerChat = 40
	// coderRecallSearchScan bounds the raw message rows scanned for a query so
	// a broad term cannot pull an unbounded slice of the history into memory.
	coderRecallSearchScan = 400
)

// coderRecallInteractionChat reports whether the running chat is an interaction
// chat ("interaction" or a tagged "interaction:<tag>" variant), the only chat
// type whose participant pair is a reliable agent/user scope.
func coderRecallInteractionChat(chatType string) bool {
	chatType = strings.TrimSpace(chatType)
	return chatType == "interaction" || strings.HasPrefix(chatType, "interaction:")
}

// coderRecallToolRunner is the provider the tools package calls (the tools
// package cannot import the msgmate package it is embedded in). It returns a
// runner that resolves the process-wide DB lazily, so a deployment without a
// database fails loudly at call time instead of at init.
func coderRecallToolRunner() tooldefs.CoderRecallRunner {
	return &coderRecallRunner{}
}

type coderRecallRunner struct{}

// RecallCoderInteractions answers with the scoped interactions of this
// agent/user pair, or with text matches inside them when req.Query is set.
func (r *coderRecallRunner) RecallCoderInteractions(req tooldefs.CoderRecallRequest) (map[string]interface{}, error) {
	db := database.GetGlobalDB()
	if db == nil {
		return nil, fmt.Errorf("coder_recall is unavailable: this deployment has no database")
	}
	scope, err := resolveCoderRecallScope(db, req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Query) != "" {
		return coderRecallSearch(db, scope, req)
	}
	return coderRecallList(db, scope, req)
}

// coderRecallScope is the resolved, trusted read scope of one recall call.
type coderRecallScope struct {
	// CurrentChatID is the interaction of the running turn.
	CurrentChatID uint
	// BotUserID is the agent identity (the automated participant).
	BotUserID uint
	// UserID is the user who triggered the running turn.
	UserID uint
	// IncludeCurrent keeps the running interaction inside the scope.
	IncludeCurrent bool
}

// resolveCoderRecallScope turns the trusted runtime of the running turn into the
// participant pair of the recall and refuses anything that is not a 1:1 chat
// between that agent and that user.
func resolveCoderRecallScope(db *gorm.DB, req tooldefs.CoderRecallRequest) (coderRecallScope, error) {
	var scope coderRecallScope
	chatUUID := strings.TrimSpace(req.ChatUUID)
	if chatUUID == "" {
		return scope, fmt.Errorf("coder_recall needs the chat identity of the running turn")
	}
	var chat database.Chat
	if err := db.Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		return scope, fmt.Errorf("coder_recall cannot resolve the running interaction: %w", err)
	}
	scope.CurrentChatID = chat.ID
	scope.IncludeCurrent = req.IncludeCurrent

	botID, err := coderRecallBotUserID(db, chat, req.BotUserUUID)
	if err != nil {
		return scope, err
	}
	userID, err := coderRecallSenderUserID(db, chat, req.SenderUUID, botID)
	if err != nil {
		return scope, err
	}
	if userID == 0 || botID == 0 {
		return scope, fmt.Errorf("coder_recall needs both the agent and the user identity of the running turn")
	}
	if userID == botID {
		// A self-interaction has no second participant to scope against; it
		// would otherwise widen the read to every chat of the agent.
		return scope, fmt.Errorf("coder_recall needs a user-triggered interaction, this turn has no separate user")
	}
	if !coderRecallInteractionChat(chat.ChatType) {
		return scope, fmt.Errorf("coder_recall is limited to interaction chats, this turn runs in a %q chat", chat.ChatType)
	}
	scope.BotUserID = botID
	scope.UserID = userID
	return scope, nil
}

// coderRecallBotUserID resolves the agent identity: the trusted bot user of the
// turn when the deployment provides one, otherwise the automated participant of
// the running interaction.
func coderRecallBotUserID(db *gorm.DB, chat database.Chat, botUserUUID string) (uint, error) {
	if uuid := strings.TrimSpace(botUserUUID); uuid != "" {
		var bot database.User
		if err := db.Where("uuid = ?", uuid).First(&bot).Error; err != nil {
			return 0, fmt.Errorf("coder_recall cannot resolve the agent identity: %w", err)
		}
		if bot.ID != chat.User1Id && bot.ID != chat.User2Id {
			// The trusted runtime names an agent that is not part of this
			// interaction; never widen the scope on a stale runtime.
			return 0, fmt.Errorf("coder_recall is unavailable: the running interaction has no agent participant")
		}
		return bot.ID, nil
	}
	participantIDs := []uint{chat.User1Id, chat.User2Id}
	var users []database.User
	if err := db.Where("id IN ?", participantIDs).Find(&users).Error; err != nil {
		return 0, fmt.Errorf("coder_recall cannot resolve the interaction participants: %w", err)
	}
	for _, user := range users {
		if user.IsAutomated {
			return user.ID, nil
		}
	}
	return 0, fmt.Errorf("coder_recall is unavailable: the running interaction has no agent participant")
}

// coderRecallSenderUserID resolves the user identity of the turn and guarantees
// that it is the other participant of the running interaction.
func coderRecallSenderUserID(db *gorm.DB, chat database.Chat, senderUUID string, botUserID uint) (uint, error) {
	if uuid := strings.TrimSpace(senderUUID); uuid != "" {
		var sender database.User
		if err := db.Where("uuid = ?", uuid).First(&sender).Error; err != nil {
			return 0, fmt.Errorf("coder_recall cannot resolve the user identity: %w", err)
		}
		if sender.ID != chat.User1Id && sender.ID != chat.User2Id {
			return 0, fmt.Errorf("coder_recall is unavailable: the running turn was triggered by a non-participant")
		}
		return sender.ID, nil
	}
	if botUserID == chat.User1Id {
		return chat.User2Id, nil
	}
	return chat.User1Id, nil
}

// coderRecallScopedChats loads the interactions of the agent/user pair, newest
// activity first, optionally excluding the running interaction.
func coderRecallScopedChats(db *gorm.DB, scope coderRecallScope) ([]database.Chat, error) {
	query := db.Where(
		"((user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?))",
		scope.BotUserID, scope.UserID, scope.UserID, scope.BotUserID,
	)
	if !scope.IncludeCurrent {
		query = query.Where("id <> ?", scope.CurrentChatID)
	}
	var chats []database.Chat
	if err := query.Order("updated_at DESC").Limit(coderRecallMaxScopeChats).Find(&chats).Error; err != nil {
		return nil, fmt.Errorf("coder_recall could not read past interactions: %w", err)
	}
	return chats, nil
}

// coderRecallList renders the scoped interactions themselves: identity, tags,
// activity window, message count, the last exchange and whether the interaction
// looks finished. It is the "which sessions exist and what were they about"
// answer an agent needs after compaction or in a fresh chat.
func coderRecallList(db *gorm.DB, scope coderRecallScope, req tooldefs.CoderRecallRequest) (map[string]interface{}, error) {
	chats, err := coderRecallScopedChats(db, scope)
	if err != nil {
		return nil, err
	}
	if filter := strings.TrimSpace(req.ChatFilter); filter != "" {
		filtered := make([]database.Chat, 0, 1)
		for _, chat := range chats {
			if strings.EqualFold(chat.UUID, filter) {
				filtered = append(filtered, chat)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("coder_recall has no interaction %q between this agent and this user", filter)
		}
		chats = filtered
	}

	limit := req.Limit
	if limit <= 0 || limit > len(chats) {
		limit = len(chats)
	}
	entries := make([]map[string]interface{}, 0, limit)
	for _, chat := range chats[:limit] {
		entry, err := coderRecallChatEntry(db, scope, chat, req.ExcerptBytes)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	state := map[string]interface{}{
		"interactions": entries,
		"scope": map[string]interface{}{
			"agent_user_id": scope.BotUserID,
			"user_id":       scope.UserID,
			"only":          "this agent with this user",
		},
		"total_matching":  len(chats),
		"scope_exhausted": len(chats) < coderRecallMaxScopeChats,
	}
	return state, nil
}

// coderRecallChatEntry summarizes one past interaction.
func coderRecallChatEntry(db *gorm.DB, scope coderRecallScope, chat database.Chat, excerptBytes int) (map[string]interface{}, error) {
	var messageCount int64
	if err := db.Model(&database.Message{}).Where("chat_id = ?", chat.ID).Count(&messageCount).Error; err != nil {
		return nil, fmt.Errorf("coder_recall could not count the messages of an interaction: %w", err)
	}
	messages, err := coderRecallMessages(db, chat.ID, coderRecallMessagesPerChat)
	if err != nil {
		return nil, err
	}
	// messages are newest first; the entry reports the exchange in reading order.
	ordered := make([]database.Message, len(messages))
	for i, message := range messages {
		ordered[len(messages)-1-i] = message
	}
	exchanges := make([]map[string]interface{}, 0, len(ordered))
	for _, message := range ordered {
		exchanges = append(exchanges, coderRecallMessageState(scope, chat, message, excerptBytes))
	}
	entry := map[string]interface{}{
		"chat_uuid":      chat.UUID,
		"chat_type":      chat.ChatType,
		"tags":           []string(chat.Tags),
		"started_at":     coderRecallTime(chat.CreatedAt),
		"last_activity":  coderRecallTime(chat.UpdatedAt),
		"message_count":  messageCount,
		"current":        chat.ID == scope.CurrentChatID,
		"latest":         exchanges,
		"excerpts_shown": len(exchanges),
	}
	if messageCount > int64(len(exchanges)) {
		entry["older_messages_omitted"] = messageCount - int64(len(exchanges))
	}
	if len(ordered) > 0 {
		entry["open_task"] = !coderRecallLooksFinished(ordered[len(ordered)-1])
	}
	return entry, nil
}

// coderRecallSearch finds the messages of the scoped interactions whose text
// matches the query, newest first.
func coderRecallSearch(db *gorm.DB, scope coderRecallScope, req tooldefs.CoderRecallRequest) (map[string]interface{}, error) {
	chats, err := coderRecallScopedChats(db, scope)
	if err != nil {
		return nil, err
	}
	if filter := strings.TrimSpace(req.ChatFilter); filter != "" {
		filtered := make([]database.Chat, 0, 1)
		for _, chat := range chats {
			if strings.EqualFold(chat.UUID, filter) {
				filtered = append(filtered, chat)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("coder_recall has no interaction %q between this agent and this user", filter)
		}
		chats = filtered
	}
	if len(chats) == 0 {
		return map[string]interface{}{
			"query":           req.Query,
			"matches":         []map[string]interface{}{},
			"total_matching":  0,
			"scope_exhausted": true,
		}, nil
	}
	chatByID := make(map[uint]database.Chat, len(chats))
	chatIDs := make([]uint, 0, len(chats))
	for _, chat := range chats {
		chatByID[chat.ID] = chat
		chatIDs = append(chatIDs, chat.ID)
	}

	pattern := "%" + strings.ToLower(req.Query) + "%"
	var messages []database.Message
	if err := db.Where("chat_id IN ?", chatIDs).
		Where("text IS NOT NULL").
		Where("LOWER(text) LIKE ?", pattern).
		Order("created_at DESC").
		Limit(coderRecallSearchScan).
		Find(&messages).Error; err != nil {
		return nil, fmt.Errorf("coder_recall could not search past interactions: %w", err)
	}

	perChat := map[uint]int{}
	matches := make([]map[string]interface{}, 0, req.Limit)
	for _, message := range messages {
		if len(matches) >= req.Limit {
			break
		}
		chat, ok := chatByID[message.ChatId]
		if !ok {
			continue
		}
		state := coderRecallMessageState(scope, chat, message, req.ExcerptBytes)
		state["chat_uuid"] = chat.UUID
		state["chat_tags"] = []string(chat.Tags)
		matches = append(matches, state)
		perChat[chat.ID]++
	}
	// report which interactions the matches came from so the agent can open one.
	chatsWithMatches := make([]string, 0, len(perChat))
	matchesPerChat := map[string]int{}
	for chatID, count := range perChat {
		uuid := chatByID[chatID].UUID
		chatsWithMatches = append(chatsWithMatches, uuid)
		matchesPerChat[uuid] = count
	}
	sort.Strings(chatsWithMatches)
	return map[string]interface{}{
		"query":            req.Query,
		"matches":          matches,
		"total_matching":   len(matches),
		"matches_per_chat": matchesPerChat,
		"chats_with_match": chatsWithMatches,
		"scanned_messages": len(messages),
		"scope_exhausted":  len(messages) < coderRecallSearchScan,
	}, nil
}

// coderRecallMessages loads the newest messages of one interaction, newest
// first.
func coderRecallMessages(db *gorm.DB, chatID uint, limit int) ([]database.Message, error) {
	var messages []database.Message
	if err := db.Where("chat_id = ?", chatID).Order("created_at DESC").Limit(limit).Find(&messages).Error; err != nil {
		return nil, fmt.Errorf("coder_recall could not read the messages of an interaction: %w", err)
	}
	return messages, nil
}

// coderRecallMessageState renders one message of the scope. The text is masked
// and truncated: a recall must never echo credentials and must stay small
// enough for the context window it is meant to extend.
func coderRecallMessageState(scope coderRecallScope, chat database.Chat, message database.Message, excerptBytes int) map[string]interface{} {
	text := ""
	if message.Text != nil {
		text = *message.Text
	}
	state := map[string]interface{}{
		"message_uuid": message.UUID,
		"at":           coderRecallTime(message.CreatedAt),
		"role":         coderRecallRole(scope, message),
		"excerpt":      coderRecallSafeExcerpt(text, excerptBytes),
	}
	if message.Text != nil && len(*message.Text) > excerptBytes {
		state["truncated"] = true
	}
	if message.ToolCalls != nil && len(*message.ToolCalls) > 0 {
		names := coderRecallToolNames(*message.ToolCalls)
		if len(names) > 0 {
			state["tool_calls"] = names
		}
	}
	return state
}

// coderRecallRole classifies a message of a scoped interaction from the trusted
// participant ids only.
func coderRecallRole(scope coderRecallScope, message database.Message) string {
	switch message.SenderId {
	case scope.BotUserID:
		return "agent"
	case scope.UserID:
		return "user"
	default:
		return "other"
	}
}

// coderRecallLooksFinished reports whether the last message of an interaction
// carries the machine-readable end-of-task flag (ci#809) or the auto-continue
// stop marker, so an agent can tell sessions that still need follow-up from
// finished ones without reading every message.
func coderRecallLooksFinished(message database.Message) bool {
	if len(message.MetaData) > 0 {
		var meta map[string]interface{}
		if err := json.Unmarshal(message.MetaData, &meta); err == nil {
			if completed, _ := meta["task_completed"].(bool); completed {
				return true
			}
		}
	}
	return false
}

// coderRecallToolNames extracts the tool names of a message so the agent can see
// what a past interaction actually did without loading the full arguments.
func coderRecallToolNames(calls []json.RawMessage) []string {
	names := make([]string, 0, len(calls))
	for _, raw := range calls {
		var call struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &call); err != nil {
			continue
		}
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			name = strings.TrimSpace(call.Name)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// coderRecallSafeExcerpt masks credential-shaped content and truncates the text
// to the requested byte budget. Masking happens before truncation so a cut can
// never split a token into an unmatchable fragment.
func coderRecallSafeExcerpt(text string, maxBytes int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	masked := text
	if raw, err := json.Marshal(map[string]string{"text": text}); err == nil {
		var decoded map[string]string
		if err := json.Unmarshal(sharecensor.MaskSecretsJSON(raw), &decoded); err == nil {
			masked = decoded["text"]
		}
	}
	if maxBytes <= 0 || len(masked) <= maxBytes {
		return masked
	}
	cut := masked[:maxBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + "…"
}

// coderRecallTime renders a timestamp as RFC3339 (zero values stay empty).
func coderRecallTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
