package queue

import (
	"fmt"
	"log"
	"time"

	"backend/workqueue"

	"github.com/hibiken/asynq"
)

// TriggerSchedulerTaskID is the stable scheduler entry/task id for the git
// trigger poll. asynq's scheduler performs leader election across instances, so
// running it in several workers/replicas does not duplicate the enqueue.
const TriggerSchedulerTaskID = "git:trigger-poll"

// StartTriggerScheduler registers and starts the periodic git trigger poll. It
// returns the running scheduler so the caller can shut it down with the worker.
func StartTriggerScheduler(connOpt asynq.RedisConnOpt, interval time.Duration) (*asynq.Scheduler, error) {
	if interval <= 0 {
		interval = time.Minute
	}
	task, err := workqueue.NewGitTriggerPollTask(workqueue.GitTriggerPollPayload{})
	if err != nil {
		return nil, fmt.Errorf("build trigger poll task: %w", err)
	}
	scheduler := asynq.NewScheduler(connOpt, &asynq.SchedulerOpts{Location: time.UTC})
	if _, err := scheduler.Register(
		fmt.Sprintf("@every %s", interval.String()),
		task,
		asynq.TaskID(TriggerSchedulerTaskID),
		asynq.Queue(QueueDefault),
	); err != nil {
		return nil, fmt.Errorf("register trigger poll schedule: %w", err)
	}
	if err := scheduler.Start(); err != nil {
		return nil, fmt.Errorf("start trigger scheduler: %w", err)
	}
	log.Printf("Started git trigger poll scheduler (every %s)", interval)
	return scheduler, nil
}
