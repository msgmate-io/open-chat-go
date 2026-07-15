package msgmate

import (
	"strings"

	"backend/api/chats"
	"backend/chatstate"
	"backend/database"
	"backend/workqueue"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// ci#845: the coordination-owner gate in front of the self-message run policy.
//
// ci#744 decides whether a self-message may be delivered at all (single active
// schedule per bot, live-turn deferral, replay and max_runs handling). ci#845
// adds the missing cross-chat rule: a chat that participates in a coordination
// lane (`owner:<ticket>:<lane>`, see backend/coordination) must not start a
// second turn on that lane while another interaction of the lane is live.
//
// The gate is deliberately opt-in: a chat that never declared an explicit lane
// tag has no owner key and is only subject to the ci#744 idle check, so
// unrelated conversations keep their historical behaviour.
const (
	// selfMessageSkipOtherOwner is the reason of a fire the coordination gate
	// deferred: an interaction of the same lane is live elsewhere.
	selfMessageSkipOtherOwner = "another interaction of the same coordination lane is live"
)

// defaultCoordinationOwnerCheck reports the live interaction of the schedule's
// coordination lane: the owner chat UUID and whether it is live. A database or
// query error never blocks a fire - the gate degrades to "no owner".
func (r *SelfMessageRuntime) defaultCoordinationOwnerCheck(schedule SelfMessageSchedule) (string, bool) {
	if r == nil || r.db == nil || strings.TrimSpace(schedule.OwnerKey) == "" {
		return "", false
	}
	var rows []database.Chat
	err := r.db.
		Where("chat_type = ? OR chat_type LIKE ?", "interaction", "interaction:%").
		Where("CAST(tags AS TEXT) LIKE ?", "%\""+schedule.OwnerKey+"\"%").
		Order("updated_at desc").
		Find(&rows).Error
	if err != nil {
		return "", false
	}
	for _, chat := range rows {
		// The schedule's own chat is the ci#744 idle check's business, not the
		// coordination gate's.
		if chat.UUID == schedule.ChatUUID {
			continue
		}
		if selfMessageInteractionLive(r.db, r.inspector, chat) {
			return chat.UUID, true
		}
	}
	return "", false
}

// selfMessageInteractionLive reports whether an interaction chat holds its turn
// right now: an active turn, a pending confirmation, or queued/pending bot:reply
// work. It mirrors the liveness rule of the agent dispatch owner-key dedupe.
func selfMessageInteractionLive(db *gorm.DB, inspector *asynq.Inspector, chat database.Chat) bool {
	if view, err := chats.ResolveInteractionStatus(db, inspector, chat); err == nil {
		if view.IsActive || view.State == string(chatstate.StateNeedsConfirmation) {
			return true
		}
	}
	return workqueue.BotReplyHasWork(inspector, chat.UUID)
}
