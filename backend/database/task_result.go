package database

// @doc:open-chat-task-results-model
// TaskResult stores persisted async task execution outcomes for admin inspection.
// Unlike Redis queue state, these records are durable in the application DB and
// can be queried through regular admin table APIs for auditing and debugging.
type TaskResult struct {
	Model
	TaskID      string `json:"task_id" gorm:"index;not null"`
	TaskType    string `json:"task_type" gorm:"index;not null"`
	Queue       string `json:"queue" gorm:"index;not null"`
	Success     bool   `json:"success"`
	Payload     string `json:"payload" gorm:"type:text"`
	Result      string `json:"result" gorm:"type:text"`
	Error       string `json:"error" gorm:"type:text"`
	Retried     int    `json:"retried"`
	MaxRetry    int    `json:"max_retry"`
	CompletedAt string `json:"completed_at" gorm:"index"`
}
