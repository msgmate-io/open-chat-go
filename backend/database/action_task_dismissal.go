package database

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
