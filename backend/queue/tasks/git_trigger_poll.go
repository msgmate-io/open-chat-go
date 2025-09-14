package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"backend/workqueue"

	"github.com/hibiken/asynq"
	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// HandleGitTriggerPoll runs a scheduled/manual provider notification poll. It
// deliberately avoids importing the git integration package directly: it
// resolves the integration through the interface registry and invokes the
// integration-owned poll_triggers function. This keeps the core-only build
// (which excludes the git integration) green.
func HandleGitTriggerPoll(_ context.Context, task *asynq.Task, deps Deps) error {
	if deps.DB == nil {
		return fmt.Errorf("%w: database unavailable", asynq.SkipRetry)
	}
	var payload workqueue.GitTriggerPollPayload
	if len(task.Payload()) > 0 {
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return fmt.Errorf("%w: invalid payload: %v", asynq.SkipRetry, err)
		}
	}
	def, ok := integrationinterface.Get("git")
	if !ok {
		return fmt.Errorf("%w: git integration not registered", asynq.SkipRetry)
	}
	fn, ok := def.Functions["poll_triggers"]
	if !ok || fn == nil {
		return fmt.Errorf("%w: poll_triggers not registered", asynq.SkipRetry)
	}
	requestPayload := map[string]interface{}{
		"db":            deps.DB,
		"backend_host":  deps.BackendHost,
		"owner_user_id": payload.OwnerUserID,
		"repository_id": payload.RepositoryID,
		"force":         payload.Force,
	}
	if _, err := fn(context.Background(), requestPayload); err != nil {
		return err
	}
	return nil
}
