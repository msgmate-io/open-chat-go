package database

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ActionTaskDismissal records that a user dismissed ("ignored / marked
// completed") a pending action task, keyed by the chat UUID and the UUID of the
// message that carries the pending action. The underlying interaction state is
// left untouched; the task merely stops surfacing in the action-task stack.
type ActionTaskDismissal struct {
	Model
	UserId  uint   `gorm:"index;uniqueIndex:idx_action_task_dismissal"`
	ChatId  uint   `gorm:"index"`
	TaskKey string `gorm:"uniqueIndex:idx_action_task_dismissal"`
}

// EnsureActionTaskDismissal records that `userID` dismissed the pending action
// task `taskKey`.
//
// ActionTaskDismissal is soft-deletable while idx_action_task_dismissal covers
// only (user_id, task_key): deleting a chat soft-deletes its dismissals
// (api/chats.Delete), leaving tombstones that the unique index still enforces.
// A plain FirstOrCreate + INSERT then failed with SQLSTATE 23505 on the next
// dismissal of a chat whose rows had been softened - the same unique-violation
// shape as the ci#1154 boot crash loop. Revive the tombstone on conflict.
func EnsureActionTaskDismissal(db *gorm.DB, userID uint, chatID uint, taskKey string) error {
	taskKey = strings.TrimSpace(taskKey)
	if db == nil || userID == 0 || taskKey == "" {
		return nil
	}
	record := ActionTaskDismissal{UserId: userID, ChatId: chatID, TaskKey: taskKey}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "task_key"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"deleted_at": nil,
			"updated_at": time.Now(),
		}),
	}).Create(&record).Error
}
