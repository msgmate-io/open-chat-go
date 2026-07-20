package workqueue

import (
	"strings"

	"github.com/hibiken/asynq"
)

// botReplyActivityPageSize is how many queue entries the snapshot reads per
// state. The chat list/state endpoints need every live task once, not per chat,
// so a single larger page is far cheaper than the per-chat scans it replaces
// (ci#850).
const botReplyActivityPageSize = 1000

// BotReplyActivity is a one-shot snapshot of which chats have a bot reply
// running or queued. The per-chat helpers (BotReplyIsActive/BotReplyQueuedCount)
// each list and filter the whole queue, so resolving N chats cost O(N x queue)
// Redis scans; the chat list and batch-state endpoints instead take one snapshot
// and answer every chat from it in O(1) (ci#850).
type BotReplyActivity struct {
	active  map[string]bool
	pending map[string]int
}

// SnapshotBotReplyActivity lists the active and pending queues once and indexes
// their bot-reply tasks by chat UUID. A nil inspector yields an empty snapshot,
// so callers degrade to "no queue activity" exactly like the nil-safe per-chat
// helpers.
func SnapshotBotReplyActivity(inspector *asynq.Inspector) *BotReplyActivity {
	snapshot := &BotReplyActivity{
		active:  map[string]bool{},
		pending: map[string]int{},
	}
	if inspector == nil {
		return snapshot
	}
	if tasks, err := inspector.ListActiveTasks(QueueDefault, asynq.PageSize(botReplyActivityPageSize)); err == nil {
		for _, task := range tasks {
			if chatUUID, ok := chatUUIDFromBotReplyTaskID(task.ID); ok {
				snapshot.active[chatUUID] = true
			}
		}
	}
	if tasks, err := inspector.ListPendingTasks(QueueDefault, asynq.PageSize(botReplyActivityPageSize)); err == nil {
		for _, task := range tasks {
			chatUUID, ok := chatUUIDFromBotReplyTaskID(task.ID)
			if !ok {
				continue
			}
			// The base task ID is the chat's own next turn, not a reply queued
			// behind a live one (ci#656): only suffixed tasks count.
			if task.ID != BotReplyTaskID(chatUUID) {
				snapshot.pending[chatUUID]++
			}
		}
	}
	return snapshot
}

// IsActive reports whether the chat has a live bot-reply turn.
func (a *BotReplyActivity) IsActive(chatUUID string) bool {
	if a == nil || chatUUID == "" {
		return false
	}
	return a.active[chatUUID]
}

// QueuedCount reports how many replies are queued behind the chat's live turn,
// mirroring BotReplyQueuedCount.
func (a *BotReplyActivity) QueuedCount(chatUUID string) int {
	if a == nil || chatUUID == "" {
		return 0
	}
	if !a.active[chatUUID] {
		return 0
	}
	return a.pending[chatUUID]
}

// chatUUIDFromBotReplyTaskID extracts the chat UUID from a bot-reply task ID
// (`bot-reply:<chatUUID>` or `bot-reply:<chatUUID>:<messageUUID>`).
func chatUUIDFromBotReplyTaskID(taskID string) (string, bool) {
	const prefix = "bot-reply:"
	if !strings.HasPrefix(taskID, prefix) {
		return "", false
	}
	rest := taskID[len(prefix):]
	if idx := strings.IndexByte(rest, ':'); idx >= 0 {
		rest = rest[:idx]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}
