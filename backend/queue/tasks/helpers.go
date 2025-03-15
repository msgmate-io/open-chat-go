package tasks

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

func writeResult(task *asynq.Task, result ToolExecutionResult) error {
	if task.ResultWriter() == nil {
		return nil
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}

	_, err = task.ResultWriter().Write(resultBytes)
	return err
}

func convertMapToJSON(data map[string]interface{}) string {
	if data == nil {
		return "{}"
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "{}"
	}

	return string(jsonBytes)
}
