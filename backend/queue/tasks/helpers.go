package tasks

import (
	"backend/database"
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
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

func persistTaskResult(DB *gorm.DB, task *asynq.Task, result ToolExecutionResult) {
	if DB == nil || task == nil {
		return
	}

	taskID := ""
	retried := 0
	maxRetry := 0
	if writer := task.ResultWriter(); writer != nil {
		taskID = writer.TaskID()
	}

	record := database.TaskResult{
		TaskID:      taskID,
		TaskType:    task.Type(),
		Queue:       QueueDefault,
		Success:     result.Success,
		Payload:     string(task.Payload()),
		Result:      result.Result,
		Error:       result.Error,
		Retried:     retried,
		MaxRetry:    maxRetry,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
	}

	_ = DB.Create(&record).Error
}
