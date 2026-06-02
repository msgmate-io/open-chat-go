package tasks

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

type TaskWithPayload struct {
	Task    *asynq.Task
	Payload ToolExecutionPayload
}

func NewToolExecutionTask(payload ToolExecutionPayload) (*TaskWithPayload, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	task := asynq.NewTask(TypeToolExecution, payloadBytes)
	return &TaskWithPayload{Task: task, Payload: payload}, nil
}
