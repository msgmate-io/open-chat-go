package chats

import (
	"context"
	"strings"

	"backend/database"
	"backend/notifications"

	"gorm.io/gorm"
)

// maybeDispatchInteractionFailureNotification emits an `interaction_error`
// notification — or `provider_credits_exhausted` when the failure detail says
// the provider is out of credits/tokens — when an automated participant
// persists a terminal *error* message for an interaction.
//
// It is the failure twin of maybeDispatchInteractionCompletedNotification and
// lives at the same choke point (`POST /chats/{chat_uuid}/messages/send`), so a
// failing turn surfaces to the operator (via the notifications integration's
// email / Signal delivery types) instead of only parking in the chat.
//
// It returns true when it handled the message (a terminal failure), so the
// caller skips the completion notification for the same message. Best-effort:
// the dispatch failure is only logged inside the notifications package.
func maybeDispatchInteractionFailureNotification(ctx context.Context, db *gorm.DB, chat database.Chat, sender, receiver database.User, message database.Message) bool {
	if db == nil {
		return false
	}
	if !sender.IsAutomated || receiver.IsAutomated || receiver.ID == 0 {
		return false
	}
	if !strings.HasPrefix(strings.TrimSpace(chat.ChatType), "interaction") {
		return false
	}
	meta, ok := decodeMessageMetadata(message.MetaData)
	if !ok || !isInteractionFailureMetadata(message, meta) {
		return false
	}
	detail := interactionFailureDetail(meta)
	if detail == "" {
		detail = "interaction turn ended on an error"
	}
	notifications.MaybeDispatchInteractionFailure(ctx, db, chat.UUID, detail)
	return true
}

// isInteractionFailureMetadata reports whether a persisted message is a
// terminal error for the interaction: it carries the error flag or a
// failure_reason, and is not an in-flight event row (e.g. a provider_retry
// indicator, which also sets finished=true but is not terminal).
func isInteractionFailureMetadata(message database.Message, meta map[string]interface{}) bool {
	if meta == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(message.DataType), "event") {
		return false
	}
	if eventType, _ := meta["event_type"].(string); strings.TrimSpace(eventType) != "" {
		return false
	}
	if errFlag, _ := meta["error"].(bool); errFlag {
		return true
	}
	if reason, _ := meta["failure_reason"].(string); strings.TrimSpace(reason) != "" {
		return true
	}
	return false
}

// interactionFailureDetail pulls the most descriptive failure text from a
// message's metadata.
func interactionFailureDetail(meta map[string]interface{}) string {
	for _, key := range []string{"error_detail", "provider_error", "failure_reason"} {
		if value, _ := meta[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
