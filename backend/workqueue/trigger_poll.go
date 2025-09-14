package workqueue

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// TypeGitTriggerPoll is the task type used to run the git integration's
// provider notification poll. The handler stays integration-agnostic: it
// resolves the "git" integration and calls its poll_triggers function, so a
// core-only build (without the git integration) still compiles.
const TypeGitTriggerPoll = "git:poll-triggers"

// GitTriggerPollPayload scopes a poll. The zero value polls every enabled
// trigger; setting OwnerUserID and/or RepositoryID limits the poll to one
// owner or one repository (both are internal numeric ids).
type GitTriggerPollPayload struct {
	OwnerUserID  uint `json:"owner_user_id,omitempty"`
	RepositoryID uint `json:"repository_id,omitempty"`
	Force        bool `json:"force,omitempty"`
}

func NewGitTriggerPollTask(payload GitTriggerPollPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeGitTriggerPoll, payloadBytes), nil
}

// EnqueueGitTriggerPoll schedules one trigger poll. The scheduled poll uses a
// stable task id so overlapping ticks coalesce.
func EnqueueGitTriggerPoll(client *asynq.Client, payload GitTriggerPollPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is required")
	}
	task, err := NewGitTriggerPollTask(payload)
	if err != nil {
		return nil, err
	}
	enqueueOpts := []asynq.Option{
		asynq.Queue(QueueDefault),
		asynq.MaxRetry(3),
		asynq.Timeout(2 * time.Minute),
		asynq.Retention(15 * time.Minute),
	}
	enqueueOpts = append(enqueueOpts, opts...)
	return client.Enqueue(task, enqueueOpts...)
}
