package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"backend/workqueue"

	"github.com/hibiken/asynq"
	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// HandleIntegrationSchedule runs an integration-owned scheduled task. It is
// deliberately integration-agnostic: it resolves the integration definition
// through the interface registry and invokes the declared function. This keeps
// integration-specific scheduling logic inside the integration and the
// core-only build (without the integration compiled in) green.
func HandleIntegrationSchedule(ctx context.Context, task *asynq.Task, deps Deps) error {
	var payload workqueue.IntegrationSchedulePayload
	if len(task.Payload()) > 0 {
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return fmt.Errorf("%w: invalid payload: %v", asynq.SkipRetry, err)
		}
	}
	payload.Integration = strings.TrimSpace(payload.Integration)
	payload.Function = strings.TrimSpace(payload.Function)
	if payload.Integration == "" || payload.Function == "" {
		return fmt.Errorf("%w: integration and function are required", asynq.SkipRetry)
	}
	def, ok := integrationinterface.Get(payload.Integration)
	if !ok {
		return fmt.Errorf("%w: %s integration not registered", asynq.SkipRetry, payload.Integration)
	}
	fn, ok := def.Functions[payload.Function]
	if !ok || fn == nil {
		return fmt.Errorf("%w: %s not registered", asynq.SkipRetry, payload.Function)
	}
	requestPayload := map[string]interface{}{}
	for key, value := range payload.Payload {
		requestPayload[key] = value
	}
	if _, exists := requestPayload["db"]; !exists {
		requestPayload["db"] = deps.DB
	}
	if _, exists := requestPayload["backend_host"]; !exists {
		requestPayload["backend_host"] = deps.BackendHost
	}
	_, err := fn(ctx, requestPayload)
	return err
}
