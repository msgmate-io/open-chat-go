package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"backend/integrations"
	"backend/workqueue"

	"github.com/hibiken/asynq"
)

// integrationScheduleFunction is the well-known Definition.Functions key an
// integration implements to declare its periodic jobs. It receives an empty
// payload and returns a list of schedule descriptors:
//
//	{"id": "poll", "function": "poll_triggers", "spec": "@every 1m",
//	 "payload": {...}, "description": "..."}
//
// Declaring schedules behind a function keeps the scheduling logic (enablement,
// interval, payload) inside the integration while the host only executes it.
// This works with the current integration interface and keeps the core-only
// build free of integration-specific imports.
const integrationScheduleFunction = "scheduled_tasks"

type integrationScheduleSpec struct {
	ID          string                 `json:"id"`
	Function    string                 `json:"function"`
	Spec        string                 `json:"spec"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
	Description string                 `json:"description,omitempty"`
}

// StartIntegrationSchedulers asks each loaded integration for its declared
// schedules (via the scheduled_tasks function) and registers them with a single
// asynq scheduler. Options are resolved by the integration itself; the host
// only executes the resulting jobs through the generic integration:schedule
// task. asynq's scheduler performs leader election across instances, so running
// it in several workers/replicas does not duplicate the enqueues.
//
// A nil scheduler is returned (with a nil error) when no integration declares an
// enabled schedule.
func StartIntegrationSchedulers(connOpt asynq.RedisConnOpt) (*asynq.Scheduler, error) {
	scheduler := asynq.NewScheduler(connOpt, &asynq.SchedulerOpts{Location: time.UTC})
	registered := 0
	for _, def := range integrations.List() {
		fn, ok := def.Functions[integrationScheduleFunction]
		if !ok || fn == nil {
			continue
		}
		raw, err := fn(context.Background(), map[string]interface{}{})
		if err != nil {
			scheduler.Shutdown()
			return nil, fmt.Errorf("integration %q %s failed: %w", def.Name, integrationScheduleFunction, err)
		}
		specs, err := decodeIntegrationSchedules(raw)
		if err != nil {
			scheduler.Shutdown()
			return nil, fmt.Errorf("integration %q %s returned invalid schedules: %w", def.Name, integrationScheduleFunction, err)
		}
		for _, spec := range specs {
			spec.ID = strings.TrimSpace(spec.ID)
			spec.Function = strings.TrimSpace(spec.Function)
			spec.Spec = strings.TrimSpace(spec.Spec)
			if spec.ID == "" || spec.Spec == "" {
				log.Printf("Integration %q declared a schedule with an empty id/spec; skipping", def.Name)
				continue
			}
			if spec.Function == "" {
				spec.Function = spec.ID
			}
			asynqTask, err := workqueue.NewIntegrationScheduleTask(workqueue.IntegrationSchedulePayload{
				Integration: def.Name,
				Function:    spec.Function,
				Payload:     spec.Payload,
			})
			if err != nil {
				scheduler.Shutdown()
				return nil, fmt.Errorf("build integration schedule task %s:%s: %w", def.Name, spec.ID, err)
			}
			taskID := def.Name + ":" + spec.ID
			if _, err := scheduler.Register(
				spec.Spec,
				asynqTask,
				asynq.TaskID(taskID),
				asynq.Queue(QueueDefault),
			); err != nil {
				scheduler.Shutdown()
				return nil, fmt.Errorf("register integration schedule %s: %w", taskID, err)
			}
			log.Printf("Registered integration schedule %s (spec %s, function %s)", taskID, spec.Spec, spec.Function)
			registered++
		}
	}
	if registered == 0 {
		return nil, nil
	}
	if err := scheduler.Start(); err != nil {
		return nil, fmt.Errorf("start integration scheduler: %w", err)
	}
	log.Printf("Started integration scheduler with %d schedule(s)", registered)
	return scheduler, nil
}

// decodeIntegrationSchedules normalizes the value returned by an integration's
// scheduled_tasks function into schedule descriptors.
func decodeIntegrationSchedules(raw interface{}) ([]integrationScheduleSpec, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var specs []integrationScheduleSpec
	if err := json.Unmarshal(encoded, &specs); err != nil {
		return nil, err
	}
	return specs, nil
}
