package chats

import (
	"backend/database"

	"gorm.io/gorm"

	"backend/logging"
)

// ClearQueuedMarkersForChat drains the ci#656 queued markers of a chat.
//
// A marker (`meta_data.queue_state == "queued"`) means "this message's reply is
// waiting behind the chat's live turn". It must be removed whenever the queued
// replies it describes are gone, otherwise the transcript keeps rendering a
// message as pending forever. Two situations drop replies without a superseding
// message:
//
//   - a new message supersedes them (the interrupt / replace delivery decisions
//     in MessageSend cancel the chat's queued replies);
//   - the user or the agent manager stops the interaction outright
//     (`workqueue.CancelBotReplyTask` on the interrupt signal and on the
//     agent-manager stop), which cancels the queued replies too.
//
// The drain is a no-op for unmarked messages, so it is safe on every such path.
func ClearQueuedMarkersForChat(DB *gorm.DB, chatID uint, chatUUID string) {
	drained, err := database.ClearMessageQueuedForChat(DB, chatID)
	if err != nil {
		logging.Errorf("ci#656: failed to drain the queued markers of chat %s: %v", chatUUID, err)
		return
	}
	if drained > 0 {
		logging.Debugf("ci#656: drained %d superseded queued marker(s) of chat %s", drained, chatUUID)
	}
}
