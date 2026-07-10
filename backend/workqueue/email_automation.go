package workqueue

import (
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

func EnqueueEmailAutomation(client *asynq.Client, payload EmailAutomationPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is required")
	}
	task, err := NewEmailAutomationTask(payload)
	if err != nil {
		return nil, err
	}
	enqueueOpts := []asynq.Option{
		asynq.Queue(QueueDefault),
		asynq.MaxRetry(10),
		asynq.Timeout(2 * time.Minute),
		asynq.Retention(24 * time.Hour),
	}
	enqueueOpts = append(enqueueOpts, opts...)
	return client.Enqueue(task, enqueueOpts...)
}
