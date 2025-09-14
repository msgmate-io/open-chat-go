package workqueue

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

// TypeIntegrationSchedule is the task type used to run an integration-owned
// scheduled task. The handler resolves the integration and function through the
// interface registry, so the host needs no integration-specific import or
// scheduling logic; a core-only build (without the integration) still compiles.
const TypeIntegrationSchedule = "integration:schedule"

// IntegrationSchedulePayload identifies the integration Function to invoke for
// one scheduled tick plus any integration-provided payload. The host injects
// its own dependencies (database, backend host) when invoking the function.
type IntegrationSchedulePayload struct {
	Integration string                 `json:"integration"`
	Function    string                 `json:"function"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
}

func NewIntegrationScheduleTask(payload IntegrationSchedulePayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeIntegrationSchedule, payloadBytes), nil
}
