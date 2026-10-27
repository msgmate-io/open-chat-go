package chats

import (
	"backend/chatstate"
	"backend/database"
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

func resolveInteractionStatus(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat) (InteractionStatusResponse, error) {
	return resolveInteractionStatusWithQueue(DB, chat, inspectorQueueActivity{inspector: inspector})
}

func resolveInteractionStatusWithQueue(DB *gorm.DB, chat database.Chat, queue chatQueueActivity) (InteractionStatusResponse, error) {
	response := InteractionStatusResponse{
		ChatUUID: chat.UUID,
		IsActive: false,
		State:    string(chatstate.StateIdle),
		Source:   "none",
	}
	response = populateAutoContinueStatus(DB, chat, response)

	// ci#656: report how many replies are queued behind the live turn so the UI
	// can render those persisted messages as pending rather than lost. Resolved
	// once here and carried through every early return below.
	response.QueuedReplies = queue.queuedCount(chat.UUID)

	// ci#850: read the transcript tail once and reuse it for both questions the
	// resolver asks of it - "is the newest message a pending user action?" and
	// "what is the newest message?" - instead of two separate round-trips.
	recent, recentErr := recentMessagesForChat(DB, chat.ID, confirmationScanLimit)

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
		if errFlag, _ := meta["error"].(bool); errFlag {
			response.State = string(chatstate.StateFailed)
		} else {
			response.State = string(chatstate.StateFinished)
		}
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
	err := DB.Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Order("id DESC").
		First(&message).Error
	return message, err
}

// recentMessagesForChat reads the newest `limit` messages of a chat, newest
// first. It is the single transcript-tail read shared by the interaction-status
// and pending-confirmation resolvers (ci#850): the ordering matches
// latestMessageForChat, so the head of the slice is the latest message.
func recentMessagesForChat(DB *gorm.DB, chatID uint, limit int) ([]database.Message, error) {
	var messages []database.Message
	err := DB.Where("chat_id = ?", chatID).
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
				actions = append(actions, ActionTaskAction{
					Kind:        ActionTaskKindRuntimeSelection,
					ActionId:    actionTaskStringField(selector, "id"),
					Title:       actionTaskFirstNonEmpty(actionTaskStringField(selector, "title"), "Runtime selection"),
					Description: actionTaskFirstNonEmpty(actionTaskStringField(selector, "description"), actionTaskStringField(selector, "reasoning")),
					Reason:      actionTaskFirstNonEmpty(actionTaskStringField(selector, "recommended"), actionTaskStringField(selector, "source")),
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

func isQueueStateActive(state string) bool {
	switch state {
	case "active", "pending", "scheduled", "retry", "processing":
		return true
	default:
		return false
	}
}
