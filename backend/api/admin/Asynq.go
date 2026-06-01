package admin

import (
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
)

type AsynqTaskItem struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Queue       string `json:"queue"`
	State       string `json:"state"`
	MaxRetry    int    `json:"max_retry"`
	Retried     int    `json:"retried"`
	LastErr     string `json:"last_error,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type AsynqTaskInfoResponse struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Queue         string `json:"queue"`
	State         string `json:"state"`
	Payload       string `json:"payload"`
	Result        string `json:"result,omitempty"`
	Retried       int    `json:"retried"`
	MaxRetry      int    `json:"max_retry"`
	LastErr       string `json:"last_error,omitempty"`
	NextProcessAt string `json:"next_process_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
}

func ListAsynqTasks(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		http.Error(w, "Asynq inspector unavailable", http.StatusInternalServerError)
		return
	}

	queueName := r.PathValue("queue")
	if queueName == "" {
		queueName = "default"
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		state = "active"
	}

	page := 0
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if parsed, parseErr := strconv.Atoi(pageParam); parseErr == nil && parsed >= 0 {
			page = parsed
		}
	}

	pageSize := 20
	if pageSizeParam := r.URL.Query().Get("page_size"); pageSizeParam != "" {
		if parsed, parseErr := strconv.Atoi(pageSizeParam); parseErr == nil && parsed > 0 && parsed <= 200 {
			pageSize = parsed
		}
	}

	opts := []asynq.ListOption{asynq.Page(page), asynq.PageSize(pageSize)}

	var tasks []*asynq.TaskInfo
	switch state {
	case "active":
		tasks, err = inspector.ListActiveTasks(queueName, opts...)
	case "pending":
		tasks, err = inspector.ListPendingTasks(queueName, opts...)
	case "scheduled":
		tasks, err = inspector.ListScheduledTasks(queueName, opts...)
	case "retry":
		tasks, err = inspector.ListRetryTasks(queueName, opts...)
	case "archived":
		tasks, err = inspector.ListArchivedTasks(queueName, opts...)
	case "completed":
		tasks, err = inspector.ListCompletedTasks(queueName, opts...)
	default:
		http.Error(w, "Invalid state, use active|pending|scheduled|retry|archived|completed", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "Failed to list tasks", http.StatusInternalServerError)
		return
	}

	items := make([]AsynqTaskItem, 0, len(tasks))
	for _, task := range tasks {
		item := AsynqTaskItem{
			ID:       task.ID,
			Type:     task.Type,
			Queue:    task.Queue,
			State:    task.State.String(),
			MaxRetry: task.MaxRetry,
			Retried:  task.Retried,
			LastErr:  task.LastErr,
		}
		if !task.CompletedAt.IsZero() {
			item.CompletedAt = task.CompletedAt.Format(time.RFC3339)
		}
		items = append(items, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"queue":     queueName,
		"state":     state,
		"page":      page,
		"page_size": pageSize,
		"tasks":     items,
	})
}

func GetAsynqTask(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		http.Error(w, "Asynq inspector unavailable", http.StatusInternalServerError)
		return
	}

	queueName := r.PathValue("queue")
	taskID := r.PathValue("task_id")
	if queueName == "" || taskID == "" {
		http.Error(w, "queue and task_id are required", http.StatusBadRequest)
		return
	}

	task, err := inspector.GetTaskInfo(queueName, taskID)
	if err != nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	response := AsynqTaskInfoResponse{
		ID:       task.ID,
		Type:     task.Type,
		Queue:    task.Queue,
		State:    task.State.String(),
		Payload:  string(task.Payload),
		Result:   string(task.Result),
		Retried:  task.Retried,
		MaxRetry: task.MaxRetry,
		LastErr:  task.LastErr,
	}

	if !task.NextProcessAt.IsZero() {
		response.NextProcessAt = task.NextProcessAt.Format(time.RFC3339)
	}
	if !task.CompletedAt.IsZero() {
		response.CompletedAt = task.CompletedAt.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func GetAsynqQueueStats(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		http.Error(w, "Asynq inspector unavailable", http.StatusInternalServerError)
		return
	}

	queueName := r.PathValue("queue")
	if queueName == "" {
		queueName = "default"
	}

	queueInfo, err := inspector.GetQueueInfo(queueName)
	if err != nil {
		http.Error(w, "Queue not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(queueInfo)
}
