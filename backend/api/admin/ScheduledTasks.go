package admin

import (
	"backend/scheduler"
	"backend/server/util"
	"encoding/json"
	"net/http"
)

// ScheduledTasksHandler handles API requests for scheduled tasks
type ScheduledTasksHandler struct {
	SchedulerService *scheduler.SchedulerService
}

// ListTasks returns all registered tasks
func (h *ScheduledTasksHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	tasks := h.SchedulerService.ListTasks()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tasks": tasks,
	})
}

// RunTask runs a task immediately
func (h *ScheduledTasksHandler) RunTask(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	taskName := r.PathValue("task_name")
	if taskName == "" {
		http.Error(w, "Task name is required", http.StatusBadRequest)
		return
	}

	err = h.SchedulerService.RunTaskNow(taskName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task started successfully",
	})
}

// Add a new task
func (h *ScheduledTasksHandler) AddTask(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var task scheduler.Task
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate the task
	if task.Name == "" || task.Schedule == "" || task.Handler == nil {
		http.Error(w, "Task must have a name, schedule, and handler", http.StatusBadRequest)
		return
	}

	// Add the task
	if err := h.SchedulerService.AddTask(task); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task added successfully",
	})
}

// Remove a task
func (h *ScheduledTasksHandler) RemoveTask(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	taskName := r.PathValue("task_name")
	if taskName == "" {
		http.Error(w, "Task name is required", http.StatusBadRequest)
		return
	}

	if err := h.SchedulerService.RemoveTask(taskName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task removed successfully",
	})
}
