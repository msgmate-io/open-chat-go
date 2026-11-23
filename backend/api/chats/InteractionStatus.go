package chats

import (
	"backend/chatstate"
	"backend/database"
	"backend/logging"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type InteractionStatusResponse struct {
	ChatUUID              string `json:"chat_uuid"`
	IsActive              bool   `json:"is_active"`
	State                 string `json:"state"`
	LatestMessageUUID     string `json:"latest_message_uuid,omitempty"`
	LatestMessageFinished *bool  `json:"latest_message_finished,omitempty"`
	Source                string `json:"source"`
	// AutoContinue reports the in-product auto-continuation loop (ci#537): the
	// interaction keeps producing turns by itself until the agent stops it.
	AutoContinue           bool   `json:"auto_continue,omitempty"`
	AutoContinueTurns      int    `json:"auto_continue_turns,omitempty"`
	AutoContinueMax        int    `json:"auto_continue_max,omitempty"`
	AutoContinueStopReason string `json:"auto_continue_stop_reason,omitempty"`
	// AutoContinueIncompleteRetries reports how many consecutive turns ended
	// incompletely and were retried, and the budget they are bounded by
	// (ci#724). A run that keeps ending without a summary stops with the
	// incomplete_retries_exhausted stop reason.
	AutoContinueIncompleteRetries    int `json:"auto_continue_incomplete_retries,omitempty"`
	AutoContinueMaxIncompleteRetries int `json:"auto_continue_max_incomplete_retries,omitempty"`
	// Stalled reports that the interaction never started: nothing is queued or
	// running for the chat and the newest message is still an unanswered prompt
	// (ci#609). StallReason names the cause, StallAgeSeconds how long the prompt
	// rested and StallThresholdSeconds the resolved stall window.
	Stalled               bool   `json:"stalled,omitempty"`
	StallReason           string `json:"stall_reason,omitempty"`
	StallAgeSeconds       int    `json:"stall_age_seconds,omitempty"`
	StallThresholdSeconds int    `json:"stall_threshold_seconds,omitempty"`
	// QueuedReplies is how many user messages are queued behind the chat's live
	// turn (ci#656). They are persisted and visible, and each one is answered
	// once the live turn completes, so the transcript can render them as
	// pending instead of them looking lost.
	QueuedReplies int `json:"queued_replies,omitempty"`
	// ContextUsage is how much of the chat's context window its newest outgoing
	// request consumed (ci#1086). It is the same indicator for cloud workflows
	// and for local/TUI coding sessions, so a session that is close to
	// compaction shows it before the turn dies. Absent when this process has no
	// recorded usage for the chat.
	ContextUsage *ContextUsageIndicator `json:"context_usage,omitempty"`
	// Todos is the todo list the agent maintains for this interaction run
	// (ci#1100). It rides the existing status poll so the chat details "Todos"
	// tab needs no extra route, and it is per run: it is read from the newest
	// `todo_write` tool call in this chat's own transcript, so nothing new is
	// persisted.
	Todos []InteractionStatusTodo `json:"todos,omitempty"`
}

// ContextUsageIndicator is the context-window usage of one chat (ci#1086).
type ContextUsageIndicator struct {
	// UsedPercent is the share of the character budget the newest request uses;
	// the UI renders it as the "x% of context used" indicator.
	UsedPercent int `json:"used_percent"`
	UsedChars   int `json:"used_chars"`
	BudgetChars int `json:"budget_chars"`
	// EstimatedTokens puts the usage in model terms.
	EstimatedTokens int `json:"estimated_tokens,omitempty"`
	// Compacted is true when the newest request had to evict or drop content to
	// fit the budget, ie compaction actually ran for this chat. A session that
	// never reports it is, by definition, not compacting (ci#1086).
	Compacted bool `json:"compacted"`
	// UpdatedAt is when the usage was recorded (RFC3339).
	UpdatedAt string `json:"updated_at,omitempty"`
}

// contextUsageForChat resolves the context-usage indicator of a chat through the
// chatstate provider registry, so this package reports usage without importing
// the AI handler (ci#1086).
func contextUsageForChat(chatUUID string) *ContextUsageIndicator {
	if strings.TrimSpace(chatUUID) == "" {
		return nil
	}
	provider, ok := chatstate.LookupContextUsageProvider()
	if !ok {
		return nil
	}
	snapshot, ok := provider(chatUUID)
	if !ok {
		return nil
	}
	return &ContextUsageIndicator{
		UsedPercent:     snapshot.UsedPercent,
		UsedChars:       snapshot.UsedChars,
		BudgetChars:     snapshot.BudgetChars,
		EstimatedTokens: snapshot.EstimatedTokens,
		Compacted:       snapshot.Compacted,
		UpdatedAt:       snapshot.UpdatedAt,
	}
}

// InteractionStatusTodo is one entry of the agent-maintained todo list.
type InteractionStatusTodo struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// todoScanLimit is how many recent messages are scanned for the agent's todo
// list when the transcript tail read for the confirmation check carries no
// `todo_write` call (ci#1100).
const todoScanLimit = 20

// latestTodoState resolves the newest todo list the agent wrote in this chat.
// The todo state is per interaction run and is never stored separately: it is
// derived from the latest `todo_write` tool call in the chat's transcript,
// reusing the tail read the status resolver already performed (ci#1100).
func latestTodoState(DB *gorm.DB, chatID uint, tail []database.Message) []InteractionStatusTodo {
	if todos, ok := todosFromMessages(tail); ok {
		return todos
	}
	messages, err := recentMessagesForChat(DB, chatID, todoScanLimit)
	if err != nil {
		return nil
	}
	todos, _ := todosFromMessages(messages)
	return todos
}

// TodoStateForChat returns the newest todo list the agent wrote in one chat.
//
// It is the exported form of latestTodoState so callers outside this package -
// eg the agent's own history tool (ci#1103) - reuse the exact same derivation
// instead of re-parsing the transcript themselves.
func TodoStateForChat(DB *gorm.DB, chatID uint) []InteractionStatusTodo {
	return latestTodoState(DB, chatID, nil)
}

// todosFromMessages returns the todo list carried by the newest `todo_write`
// call (newest message first, newest call last within a message). The boolean
// reports whether such a call was found at all, so an agent that wrote an empty
// list is distinguishable from a chat that never touched the todo tool.
func todosFromMessages(messages []database.Message) ([]InteractionStatusTodo, bool) {
	for _, message := range messages {
		if message.ToolCalls == nil {
			continue
		}
		calls := *message.ToolCalls
		for i := len(calls) - 1; i >= 0; i-- {
			if todos, ok := todosFromToolCall(calls[i]); ok {
				return todos, true
			}
		}
	}
	return nil, false
}

func todosFromToolCall(raw json.RawMessage) ([]InteractionStatusTodo, bool) {
	var call map[string]interface{}
	if json.Unmarshal(raw, &call) != nil {
		return nil, false
	}
	if !isTodoWriteToolName(toolCallName(call)) {
		return nil, false
	}
	args := toolCallArguments(call)
	if args == nil {
		return nil, false
	}
	rawTodos, ok := args["todos"].([]interface{})
	if !ok {
		return nil, false
	}
	todos := make([]InteractionStatusTodo, 0, len(rawTodos))
	for _, item := range rawTodos {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		text := strings.TrimSpace(actionTaskFirstNonEmpty(
			actionTaskStringField(entry, "text"),
			actionTaskStringField(entry, "content"),
			actionTaskStringField(entry, "title"),
		))
		if text == "" {
			continue
		}
		todos = append(todos, InteractionStatusTodo{
			Text:   text,
			Status: normalizeTodoStatus(actionTaskStringField(entry, "status")),
		})
	}
	return todos, true
}

// toolCallName resolves the tool name of a persisted call, accepting the bare
// OpenAI tool shape that nests it under `function`.
func toolCallName(call map[string]interface{}) string {
	name := actionTaskFirstNonEmpty(actionTaskStringField(call, "name"), actionTaskStringField(call, "tool_name"))
	if name != "" {
		return name
	}
	if fn, ok := call["function"].(map[string]interface{}); ok {
		return actionTaskFirstNonEmpty(actionTaskStringField(fn, "name"), actionTaskStringField(fn, "tool_name"))
	}
	return ""
}

// toolCallArguments decodes a persisted call's arguments, which arrive either as
// an already-decoded object or as the JSON string the bridge marshals.
func toolCallArguments(record map[string]interface{}) map[string]interface{} {
	switch args := record["arguments"].(type) {
	case map[string]interface{}:
		return args
	case string:
		trimmed := strings.TrimSpace(args)
		if trimmed == "" {
			return nil
		}
		var decoded map[string]interface{}
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			return decoded
		}
		return nil
	}
	if fn, ok := record["function"].(map[string]interface{}); ok {
		return toolCallArguments(fn)
	}
	return nil
}

func isTodoWriteToolName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.TrimPrefix(normalized, "coder_")
	return normalized == "todo_write"
}

func normalizeTodoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done":
		return "completed"
	case "in_progress", "in-progress", "active", "running":
		return "in_progress"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "pending"
	}
}

// GetInteractionStatus returns deterministic status for a private interaction chat.
//
//	@Summary      Get interaction status
//	@Description  Retrieve deterministic active/finished status for an interaction chat
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} InteractionStatusResponse "Interaction status"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Failure      500 {string} string "Unable to resolve interaction status"
//	@Router       /api/v1/chats/{chat_uuid}/status [get]
func (h *ChatsHandler) GetInteractionStatus(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		writeChatErrorJSON(w, http.StatusBadRequest, "Unable to get database or user")
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		writeChatErrorJSON(w, http.StatusInternalServerError, "Async queue unavailable")
		return
	}

	chatUUID, chatUUIDErr := parseChatUUID(r)
	if chatUUIDErr != nil {
		writeChatErrorJSON(w, http.StatusBadRequest, "Invalid chat UUID")
		return
	}

	chat, err := findAccessibleChat(DB, user, chatUUID)
	if err != nil {
		writeChatErrorJSON(w, http.StatusNotFound, "Chat not found")
		return
	}

	if !enforceBrowserTokenInteractionChat(w, r, chat.ChatType) {
		return
	}

	status, err := resolveInteractionStatus(DB, inspector, chat)
	if err != nil {
		writeChatErrorJSON(w, http.StatusInternalServerError, "Unable to resolve interaction status")
		return
	}
	// ci#609: an interaction that cannot provision must never rest in a silent
	// idle. The status read is the point an operator (or the dispatcher loop)
	// looks at, so the stall is surfaced here as a visible, persisted bot error.
	// It is idempotent: a chat gets at most one stall report.
	if status.Stalled {
		ReportInteractionStall(DB, inspector, chat)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// GetSharedInteractionStatus returns deterministic status for a shared interaction.
//
//	@Summary      Get shared interaction status
//	@Description  Retrieve deterministic active/finished status for a shared interaction chat
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        chat_share_uuid path string true "Shared chat UUID"
//	@Success      200 {object} InteractionStatusResponse "Interaction status"
//	@Failure      400 {string} string "Invalid shared chat UUID"
//	@Failure      404 {string} string "Shared chat not found"
//	@Failure      500 {string} string "Unable to resolve interaction status"
//	@Router       /api/interaction/{chat_share_uuid}/status [get]
func (h *ChatsHandler) GetSharedInteractionStatus(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		writeChatErrorJSON(w, http.StatusBadRequest, "Unable to get database")
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		writeChatErrorJSON(w, http.StatusInternalServerError, "Async queue unavailable")
		return
	}

	shareUUID := strings.TrimSpace(r.PathValue("chat_share_uuid"))
	if shareUUID == "" {
		writeChatErrorJSON(w, http.StatusBadRequest, "Invalid shared chat UUID")
		return
	}

	chat, share, err := getSharedChatByUUID(DB, shareUUID)
	if err != nil {
		writeChatErrorJSON(w, http.StatusNotFound, "Shared chat not found")
		return
	}

	if !shareAllowsUser(share, util.GetOptionalUser(r)) {
		writeChatErrorJSON(w, http.StatusNotFound, "Shared chat not found")
		return
	}

	status, err := resolveInteractionStatus(DB, inspector, chat)
	if err != nil {
		writeChatErrorJSON(w, http.StatusInternalServerError, "Unable to resolve interaction status")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// confirmationScanLimit is how many recent messages are scanned for pending
// user confirmations (the confirming message is usually the latest bot
// message, but a newer user message may sit on top of it).
const confirmationScanLimit = 5

// chatQueueActivity answers the two live-queue questions the resolver asks of a
// chat. The default implementation queries the asynq inspector per chat; the
// chat-list and batch-state endpoints pass a BotReplyActivity snapshot so N
// chats cost one queue listing instead of N (ci#850).
type chatQueueActivity interface {
	isActive(chatUUID string) bool
	queuedCount(chatUUID string) int
}

type inspectorQueueActivity struct{ inspector *asynq.Inspector }

func (a inspectorQueueActivity) isActive(chatUUID string) bool {
	return a.inspector != nil && workqueue.BotReplyIsActive(a.inspector, chatUUID)
}

func (a inspectorQueueActivity) queuedCount(chatUUID string) int {
	return workqueue.BotReplyQueuedCount(a.inspector, chatUUID)
}

type snapshotQueueActivity struct{ snapshot *workqueue.BotReplyActivity }

func (a snapshotQueueActivity) isActive(chatUUID string) bool {
	return a.snapshot.IsActive(chatUUID)
}

func (a snapshotQueueActivity) queuedCount(chatUUID string) int {
	return a.snapshot.QueuedCount(chatUUID)
}

// ResolveInteractionStatus exposes the deterministic interaction-state resolver
// to other core packages (the trusted agent-dispatch surface) so a dispatched
// agent's live state is computed by the same logic the chat status endpoint uses.
func ResolveInteractionStatus(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat) (InteractionStatusResponse, error) {
	return resolveInteractionStatus(DB, inspector, chat)
}

// ResolveInteractionStatusesWithSnapshot resolves the live status of many chats
// using a single BotReplyActivity snapshot, so a listing of N interactions
// costs one queue listing instead of one asynq inspector round-trip (x2) per
// chat (ci#850). The result aligns 1:1 with the input order; a per-chat
// resolution error degrades to a plain idle status rather than failing the
// whole listing.
func ResolveInteractionStatusesWithSnapshot(DB *gorm.DB, snapshot *workqueue.BotReplyActivity, chats []database.Chat) []InteractionStatusResponse {
	queue := snapshotQueueActivity{snapshot: snapshot}
	// One transcript-tail query for the whole batch instead of one per chat.
	tails := loadChatMessageTails(DB, chats)
	out := make([]InteractionStatusResponse, len(chats))
	for i, chat := range chats {
		status, err := resolveInteractionStatusWithQueue(DB, chat, queue, tails)
		if err != nil {
			out[i] = InteractionStatusResponse{
				ChatUUID: chat.UUID,
				State:    string(chatstate.StateIdle),
				Source:   "none",
			}
			continue
		}
		out[i] = status
	}
	return out
}

func resolveInteractionStatus(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat) (InteractionStatusResponse, error) {
	return resolveInteractionStatusWithQueue(DB, chat, inspectorQueueActivity{inspector: inspector}, nil)
}

func resolveInteractionStatusWithQueue(DB *gorm.DB, chat database.Chat, queue chatQueueActivity, tails chatMessageTails) (InteractionStatusResponse, error) {
	response := InteractionStatusResponse{
		ChatUUID: chat.UUID,
		IsActive: false,
		State:    string(chatstate.StateIdle),
		Source:   "none",
	}
	response = populateAutoContinueStatus(DB, chat, response)

	// ci#1086: report how much of the context window the chat's newest request
	// consumed. Resolved here, before the early returns below, so the indicator
	// is present for every interaction state (active, needs_confirmation,
	// finished, idle) and for local/TUI sessions exactly like cloud workflows.
	response.ContextUsage = contextUsageForChat(chat.UUID)

	// ci#656: report how many replies are queued behind the live turn so the UI
	// can render those persisted messages as pending rather than lost. Resolved
	// once here and carried through every early return below.
	response.QueuedReplies = queue.queuedCount(chat.UUID)

	// ci#850: read the transcript tail once and reuse it for both questions the
	// resolver asks of it - "is the newest message a pending user action?" and
	// "what is the newest message?" - instead of two separate round-trips.
	recent, recentErr, tailKnown := tails.lookup(chat.ID)
	if !tailKnown {
		recent, recentErr = recentMessagesForChat(DB, chat.ID, confirmationScanLimit)
	}

	// ci#1100: the agent-maintained todo list of this interaction run rides the
	// same status read, so the chat details "Todos" tab is a pure render of the
	// existing poll. Best effort: a missing list just renders the empty state.
	response.Todos = latestTodoState(DB, chat.ID, recent)

	// Pending user actions take precedence over backend activity: when the
	// agent is waiting on the user (a confirmable action, an opencode
	// permission prompt or an ask-only relay question) the interaction is not
	// "active work" even if the backend session is still alive. This is what
	// keeps the blue "needs confirmation" state visible while an ask-only
	// question is paused, instead of the running backend short-circuit hiding
	// it. Runs before the backend/queue checks below on purpose.
	if recentErr == nil && messagesHavePendingConfirmation(recent) {
		if len(recent) > 0 {
			response.LatestMessageUUID = recent[0].UUID
		}
		response.State = string(chatstate.StateNeedsConfirmation)
		response.IsActive = false
		response.Source = "message_meta"
		return response, nil
	}

	// External chat backends (eg opencode) run generations outside the bot
	// reply queue, so their live activity is reported through a state
	// provider instead of the queue.
	if backend, ok := chatBackendName(DB, chat); ok {
		if provider, ok := chatstate.LookupBackendStateProvider(backend); ok {
			if state, ok := provider(chat.UUID); ok && state == chatstate.BackendStateRunning {
				response.IsActive = true
				response.State = string(chatstate.StateActive)
				response.Source = "chat_backend"
				return response, nil
			}
		}
	}

	if queue.isActive(chat.UUID) {
		response.IsActive = true
		response.State = string(chatstate.StateActive)
		response.Source = "queue"
		return response, nil
	}

	var latest database.Message
	var err error
	if recentErr == nil && len(recent) > 0 {
		// Reuse the tail read above: it is newest-first, so its head is the
		// latest message and needs no second query (ci#850).
		latest = recent[0]
	} else {
		latest, err = latestMessageForChat(DB, chat.ID)
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response, nil
		}
		return InteractionStatusResponse{}, err
	}

	response.LatestMessageUUID = latest.UUID
	response.Source = "message_meta"

	meta := map[string]interface{}{}
	if len(latest.MetaData) > 0 {
		_ = json.Unmarshal(latest.MetaData, &meta)
	}

	finished, _ := meta["finished"].(bool)
	response.LatestMessageFinished = &finished

	// A finished bot message that still carries a pending user confirmation was
	// already handled by the early needs_confirmation check above.
	if finished {
		response.State = string(interactionStateForFinishedMetadata(meta))
		response.IsActive = false
		return response, nil
	}

	// ci#609: reaching here means nothing is live for the chat (no queue task, no
	// running backend, no pending user action) and the newest message is not a
	// finished agent turn. An interaction in that state is either genuinely
	// resting or it never started at all - the silent freeze. Distinguish the two
	// from the newest message's age so a stalled interaction is reported with a
	// reason instead of looking like a healthy idle chat.
	if stall := interactionStallFromMessages(DB, chat, time.Now()); stall.Stalled {
		response.Stalled = true
		response.StallReason = stall.Reason
		response.StallAgeSeconds = int(stall.Age / time.Second)
		response.StallThresholdSeconds = int(stall.Threshold / time.Second)
	}
	response.State = string(chatstate.StateIdle)
	response.IsActive = false
	return response, nil
}

// chatBackendName returns the external chat backend name from the chat's
// shared config ("chat_backend" key, with a legacy fallback to "backend").
func chatBackendName(DB *gorm.DB, chat database.Chat) (string, bool) {
	var configData []byte
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		configData = chat.SharedConfig.ConfigData
	} else {
		var sharedConfig database.SharedChatConfig
		if err := DB.Where("chat_id = ?", chat.ID).First(&sharedConfig).Error; err != nil || len(sharedConfig.ConfigData) == 0 {
			return "", false
		}
		configData = sharedConfig.ConfigData
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(configData, &config); err != nil {
		return "", false
	}
	if name := chatstate.ChatBackendNameFromConfig(config); name != "" {
		return name, true
	}
	return "", false
}

// latestMessageForChat returns the chat's most recent message. The id is the
// tiebreak: a batch of messages persisted in the same transaction (or in the
// same second) shares created_at, and "the newest message" must still be
// deterministic for the status/stall resolvers (ci#609).
func latestMessageForChat(DB *gorm.DB, chatID uint) (database.Message, error) {
	var message database.Message
	// Same projection as the tail read: `content`/`reasoning` are TOAST-resident
	// and this runs once per interaction chat from the stall guard.
	err := DB.Select(messageTailColumns).
		Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Order("id DESC").
		Limit(1).
		Find(&message).Error
	if err == nil && message.ID == 0 {
		return message, gorm.ErrRecordNotFound
	}
	return message, err
}

// recentMessagesForChat reads the newest `limit` messages of a chat, newest
// first. It is the single transcript-tail read shared by the interaction-status
// and pending-confirmation resolvers (ci#850): the ordering matches
// latestMessageForChat, so the head of the slice is the latest message.
// chatMessageTails carries the transcript tail of every chat of a batch, so a
// listing of N chats costs ONE message query instead of N (each per-chat read
// was a separate round-trip with its own planning, and the chat list plus the
// /api/v1/chats/states poll resolve up to 100 chats per request - that N+1 was
// the dominant cost of the sidebar).
//
// A chat whose tail was loaded but which has no messages is present with an
// empty slice, so the resolver does not fall back to a per-chat query for it.
type chatMessageTails map[uint][]database.Message

// lookup returns the tail for a chat. ok is false when the batch does not
// cover that chat (no batch, or a load failure), in which case the caller reads
// it per chat.
func (t chatMessageTails) lookup(chatID uint) ([]database.Message, error, bool) {
	if t == nil {
		return nil, nil, false
	}
	messages, ok := t[chatID]
	if !ok {
		return nil, nil, false
	}
	return messages, nil, true
}

// loadChatMessageTails reads the transcript tails for a batch of chats. A
// failure is logged and reported as "no batch" so callers degrade to the
// per-chat reads instead of failing the request.
func loadChatMessageTails(DB *gorm.DB, chats []database.Chat) chatMessageTails {
	if DB == nil || len(chats) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(chats))
	for _, chat := range chats {
		if chat.ID != 0 {
			ids = append(ids, chat.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	tails, err := recentMessagesByChatLimit(DB, ids, confirmationScanLimit)
	if err != nil {
		logging.Errorf("Failed to batch-load chat transcript tails (%d chats), falling back to per-chat reads: %v", len(ids), err)
		return nil
	}
	for _, id := range ids {
		if _, ok := tails[id]; !ok {
			tails[id] = []database.Message{}
		}
	}
	return chatMessageTails(tails)
}

func recentMessagesForChat(DB *gorm.DB, chatID uint, limit int) ([]database.Message, error) {
	var messages []database.Message
	err := DB.Select(messageTailColumns).
		Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Order("id DESC").
		Limit(limit).
		Find(&messages).Error
	return messages, err
}

func hasPendingConfirmationInRecentMessages(DB *gorm.DB, chatID uint) bool {
	messages, err := recentMessagesForChat(DB, chatID, confirmationScanLimit)
	if err != nil {
		return false
	}
	return messagesHavePendingConfirmation(messages)
}

// messagesHavePendingConfirmation reports whether any of the given (newest-first)
// messages still carries a pending user action. It is the slice-level form of
// hasPendingConfirmationInRecentMessages, so a caller that already read the
// transcript tail does not have to answer the question with a second query.
func messagesHavePendingConfirmation(messages []database.Message) bool {
	for _, message := range messages {
		if messageHasPendingConfirmation(message) {
			return true
		}
	}
	return false
}

func messageHasPendingConfirmation(message database.Message) bool {
	// ci#735: a rejected interaction confirmation stays on the stack (so it can
	// be re-run), but it is not a pending confirmation any more - the user has
	// already decided, they merely kept the option to void that decision.
	for _, action := range pendingActionsForMessage(message) {
		if !action.Rejected {
			return true
		}
	}
	return false
}

// pendingActionsForMessage extracts every user-facing pending action carried by
// a message. It is the single source of truth for both the per-chat
// needs_confirmation state and the action-task stack.
func pendingActionsForMessage(message database.Message) []ActionTaskAction {
	actions := []ActionTaskAction{}
	if len(message.MetaData) > 0 {
		meta := map[string]interface{}{}
		if json.Unmarshal(message.MetaData, &meta) == nil {
			if rawActions, ok := meta["confirmable_actions"].([]interface{}); ok {
				for _, rawAction := range rawActions {
					action, ok := rawAction.(map[string]interface{})
					if !ok {
						continue
					}
					if status, _ := action["status"].(string); status == "pending" {
						actionInput, _ := action["input"].(map[string]interface{})
						continueAfterExecute, _ := action["continue_after_execute"].(bool)
						actions = append(actions, ActionTaskAction{
							Kind:                 ActionTaskKindConfirmableAction,
							ActionId:             actionTaskStringField(action, "action_id"),
							Title:                actionTaskStringField(action, "title"),
							Description:          actionTaskStringField(action, "description"),
							TargetToolName:       actionTaskStringField(action, "target_tool_name"),
							DangerLevel:          actionTaskStringField(action, "danger_level"),
							Input:                actionInput,
							ConfirmLabel:         actionTaskStringField(action, "confirm_label"),
							ContinueAfterExecute: continueAfterExecute,
						})
					}
				}
			}
			if permission, ok := meta["opencode_permission"].(map[string]interface{}); ok {
				if status, _ := permission["status"].(string); status == "pending" {
					actions = append(actions, ActionTaskAction{
						Kind:        ActionTaskKindOpencodePermission,
						Title:       actionTaskFirstNonEmpty(actionTaskStringField(permission, "title"), "OpenCode permission request"),
						Description: actionTaskStringField(permission, "description"),
						Reason:      actionTaskFirstNonEmpty(actionTaskStringField(permission, "reason"), actionTaskStringField(permission, "permission_type")),
					})
				}
			}
			// An ask-only relay question leaves a pending opencode_question
			// marker until the user answers it; it requires user action, so the
			// interaction renders as the blue "needs confirmation" state and the
			// question joins the action-task stack.
			if question, ok := meta["opencode_question"].(map[string]interface{}); ok {
				if status, _ := question["status"].(string); status == "pending" {
					actions = append(actions, ActionTaskAction{
						Kind:        ActionTaskKindOpencodeQuestion,
						Title:       actionTaskFirstNonEmpty(actionTaskStringField(question, "title"), "OpenCode question"),
						Description: actionTaskStringField(question, "description"),
						Reason:      actionTaskFirstNonEmpty(actionTaskStringField(question, "reason"), actionTaskStringField(question, "id")),
					})
				}
			}
			// A terminal OpenCode error (build/plan failure) leaves a pending
			// opencode_needs_action marker; it requires user action, so the
			// interaction renders as the blue "needs confirmation" state.
			if needsAction, ok := meta["opencode_needs_action"].(map[string]interface{}); ok {
				if status, _ := needsAction["status"].(string); status == "pending" {
					actions = append(actions, ActionTaskAction{
						Kind:        ActionTaskKindOpencodeNeedsAction,
						Title:       actionTaskFirstNonEmpty(actionTaskStringField(needsAction, "title"), "OpenCode needs action"),
						Description: actionTaskFirstNonEmpty(actionTaskStringField(needsAction, "message"), actionTaskStringField(needsAction, "reason")),
						Reason:      actionTaskStringField(needsAction, "reason"),
					})
				}
			}
			// Interaction confirmation gate: the bot created the chat but waits
			// for the user to approve the deferred interaction.
			if confirmation, ok := meta[interactionConfirmationMetaKey].(map[string]interface{}); ok {
				status, _ := confirmation["status"].(string)
				action := ActionTaskAction{
					Kind:        ActionTaskKindInteractionConfirmation,
					Title:       actionTaskFirstNonEmpty(actionTaskStringField(confirmation, "title"), "Interaction confirmation"),
					Description: actionTaskStringField(confirmation, "description"),
				}
				// ci#735: a rejected gate is a terminal-looking error state, but
				// the user can void the rejection and re-dispatch the
				// interaction. Expose that as an explicit re-run affordance
				// instead of the raw "rejected" error.
				if status == InteractionConfirmationRejected {
					action.Rejected = true
					action.Rerunnable = true
					action.ActionId = ActionTaskActionRerunInteractionConfirmation
					action.ConfirmLabel = "Re-run interaction"
					action.RerunOf = actionTaskStringField(confirmation, "rerun_of")
					action.RerunCount = actionTaskIntField(confirmation, "rerun_count")
				}
				// ci#735: a re-run gate carries the re-run bookkeeping into the
				// task so the UI can show that this confirmation re-opens a
				// previously rejected interaction.
				if rerunOf := actionTaskStringField(confirmation, "rerun_of"); rerunOf != "" {
					action.RerunOf = rerunOf
					action.RerunCount = actionTaskIntField(confirmation, "rerun_count")
				}
				if status == InteractionConfirmationPending || status == InteractionConfirmationRejected {
					actions = append(actions, action)
				}
			}
			// Runtime-selector proposals: a selector bot asks the user which
			// runtime should handle the request. Until the user starts or
			// cancels a choice the interaction is waiting on them.
			for _, selector := range parseRuntimeSelectorsFromMeta(meta) {
				status, _ := selector["status"].(string)
				if status != "" && status != RuntimeSelectorPending {
					continue
				}
				// ci#1135: carry the reset affordance so the widget can hide
				// the button without an extra fetch. Only an explicit
				// allow_reset:false reports false; a missing key means allowed.
				allowReset := runtimeSelectorAllowsReset(selector)
				actions = append(actions, ActionTaskAction{
					Kind:        ActionTaskKindRuntimeSelection,
					ActionId:    actionTaskStringField(selector, "id"),
					Title:       actionTaskFirstNonEmpty(actionTaskStringField(selector, "title"), "Runtime selection"),
					Description: actionTaskFirstNonEmpty(actionTaskStringField(selector, "description"), actionTaskStringField(selector, "reasoning")),
					Reason:      actionTaskFirstNonEmpty(actionTaskStringField(selector, "recommended"), actionTaskStringField(selector, "source")),
					AllowReset:  &allowReset,
				})
			}
		}
	}
	if message.ToolCalls != nil {
		for _, rawToolCall := range *message.ToolCalls {
			toolCall := map[string]interface{}{}
			if json.Unmarshal(rawToolCall, &toolCall) != nil {
				continue
			}
			if status, _ := toolCall["status"].(string); status == "pending_confirmation" {
				actions = append(actions, ActionTaskAction{
					Kind:           ActionTaskKindToolConfirmation,
					ActionId:       actionTaskFirstNonEmpty(actionTaskStringField(toolCall, "id"), actionTaskStringField(toolCall, "call_id")),
					Title:          actionTaskFirstNonEmpty(actionTaskStringField(toolCall, "title"), "Tool call confirmation"),
					Description:    actionTaskStringField(toolCall, "description"),
					TargetToolName: actionTaskFirstNonEmpty(actionTaskStringField(toolCall, "name"), actionTaskStringField(toolCall, "tool_name")),
				})
			}
		}
	}
	return actions
}

// nonFaultTerminalFailureReasons lists the recorded terminal failure reasons
// that must never settle an interaction as failed even when the `error` flag is
// set on the message.
//
// tool_call_budget_exhausted: the server stopped the turn by design to protect
// the chat and the sandbox (ci#1160).
//
// provider_backpressure: the turn never reached the provider because OpenChat's
// own per-key admission queue was saturated, so the server refused the request
// itself instead of a provider failing it; ci#1179 schedules one bounded
// recovery continuation for it.
//
// msgmate.failureReasonExemptFromErrorFlag keeps both reasons out of the error
// surface for new turns; the check here also covers messages persisted before
// that exemption landed, so a healthy capacity stop is never displayed as a
// defect.
var nonFaultTerminalFailureReasons = map[string]bool{
	// msgmate.FailureReasonToolCallBudget
	"tool_call_budget_exhausted": true,
	// msgmate.FailureReasonProviderBackpressure
	"provider_backpressure": true,
}

// InteractionStatusProviderBackpressureContinuationsKey mirrors
// msgmate.ProviderBackpressureContinuationsMetadataKey (ci#1179): the number of
// bounded provider-backpressure recovery continuations already spent on the
// turn. A non-zero value means the stop was recovered by scheduling a recovery
// turn rather than being a dead end.
const InteractionStatusProviderBackpressureContinuationsKey = "ci1179_backpressure_continuations"

// IsNonFaultTerminalFailureReason reports whether a recorded terminal failure
// reason describes a capacity stop the server performed on itself, so it must
// never settle an interaction as failed.
//
// It is exported so the msgmate layer, which owns the canonical reason
// constants, can assert in one test that the two definitions agree. The
// exemption is duplicated here on purpose: chats must not import msgmate
// (msgmate imports chats), and the check also has to cover messages persisted
// before the msgmate exemption landed.
func IsNonFaultTerminalFailureReason(reason string) bool {
	return nonFaultTerminalFailureReasons[reason]
}

// interactionStateForFinishedMetadata decides the settled state of a finished
// interaction from the metadata of its newest message.
//
// `finished && error` is a failure, but only for reasons that are actually
// faults: a capacity stop (a reached tool-call budget or a provider-backpressure
// refusal, recovered or not) ended the turn without anything being broken, so it
// settles `finished` and stays fully inspectable through `failure_reason` /
// `turn_ended` / `capacity_stop` (ci#1160, ci#1179).
func interactionStateForFinishedMetadata(meta map[string]interface{}) chatstate.State {
	errFlag, _ := meta["error"].(bool)
	if !errFlag {
		return chatstate.StateFinished
	}
	if reason, _ := meta["failure_reason"].(string); nonFaultTerminalFailureReasons[reason] {
		return chatstate.StateFinished
	}
	// ci#1160/ci#1179: a stamped capacity stop (tool-call budget or provider
	// backpressure) is never a fault, whatever the failure reason says.
	if stop, _ := meta["capacity_stop"].(string); stop != "" {
		return chatstate.StateFinished
	}
	// ci#1179: a back-pressure stop whose single bounded recovery turn was
	// actually enqueued is a recovered turn, not a dead end.
	switch recovery := meta[InteractionStatusProviderBackpressureContinuationsKey].(type) {
	case float64:
		if recovery > 0 {
			return chatstate.StateFinished
		}
	case int:
		if recovery > 0 {
			return chatstate.StateFinished
		}
	}
	return chatstate.StateFailed
}

func isQueueStateActive(state string) bool {
	switch state {
	case "active", "pending", "scheduled", "retry", "processing":
		return true
	default:
		return false
	}
}
