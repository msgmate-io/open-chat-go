package queue

import "backend/queue/tasks"

const (
	QueueDefault      = tasks.QueueDefault
	TypeToolExecution = tasks.TypeToolExecution
)

type ToolExecutionPayload = tasks.ToolExecutionPayload
type ToolExecutionResult = tasks.ToolExecutionResult
type TaskWithPayload = tasks.TaskWithPayload

var NewToolExecutionTask = tasks.NewToolExecutionTask
