package scheduler

import (
	"backend/database"
	"context"
	"fmt"
	"github.com/go-co-op/gocron"
	"gorm.io/gorm"
	"log"
	"time"
)

// SchedulerService manages all scheduled tasks
type SchedulerService struct {
	scheduler       *gocron.Scheduler
	DB              *gorm.DB
	ctx             context.Context
	cancel          context.CancelFunc
	registeredTasks map[string]Task
}

// NewSchedulerService creates a new scheduler service
func NewSchedulerService(DB *gorm.DB) *SchedulerService {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a scheduler with UTC timezone
	s := gocron.NewScheduler(time.UTC)

	service := &SchedulerService{
		scheduler:       s,
		DB:              DB,
		ctx:             ctx,
		cancel:          cancel,
		registeredTasks: make(map[string]Task),
	}

	return service
}

// Start begins running the scheduler
func (s *SchedulerService) Start() {
	log.Println("Starting scheduler service...")
	//s.InitializeIntegrationTasks()
	s.scheduler.StartAsync()
}

// Stop halts all scheduled jobs
func (s *SchedulerService) Stop() {
	log.Println("Stopping scheduler service...")
	s.scheduler.Stop()
	s.cancel()
}

// RegisterTasks sets up all scheduled tasks
func (s *SchedulerService) RegisterTasks() {
	// Register system maintenance tasks
	s.registerTaskGroup(SystemMaintenanceTasks(s.DB))

	// Register data maintenance tasks
	s.registerTaskGroup(DataMaintenanceTasks(s.DB))

	log.Printf("Registered %d scheduled tasks", len(s.registeredTasks))
}

// registerTaskGroup registers a group of tasks
func (s *SchedulerService) registerTaskGroup(tasks []Task) {
	for _, task := range tasks {
		if !task.Enabled {
			log.Printf("Skipping disabled task: %s", task.Name)
			continue
		}

		s.registerTask(task)
	}
}

// registerTask registers a single task with the scheduler
func (s *SchedulerService) registerTask(task Task) {
	// Store the task in our registry
	s.registeredTasks[task.Name] = task

	// Parse the cron schedule
	job, err := s.scheduler.Cron(task.Schedule).Do(func() {
		log.Printf("Running scheduled task: %s - %s", task.Name, task.Description)

		if err := task.Handler(); err != nil {
			log.Printf("Error in task %s: %v", task.Name, err)
		} else {
			log.Printf("Task %s completed successfully", task.Name)
		}
	})

	if err != nil {
		log.Printf("Error scheduling task %s: %v", task.Name, err)
		return
	}

	// Set job metadata
	job.Tag(task.Name)

	log.Printf("Registered task: %s (%s)", task.Name, task.Schedule)
}

// GetTaskByName returns a task by its name
func (s *SchedulerService) GetTaskByName(name string) (Task, bool) {
	task, exists := s.registeredTasks[name]
	return task, exists
}

// ListTasks returns all registered tasks
func (s *SchedulerService) ListTasks() []Task {
	tasks := make([]Task, 0, len(s.registeredTasks))
	for _, task := range s.registeredTasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// RunTaskNow runs a task immediately by name
func (s *SchedulerService) RunTaskNow(name string) error {
	task, exists := s.registeredTasks[name]
	if !exists {
		return fmt.Errorf("task %s not found", name)
	}

	return task.Handler()
}

// Task implementations

// cleanExpiredSessions removes expired sessions from the database
func (s *SchedulerService) cleanExpiredSessions() {
	result := s.DB.Where("expiry < ?", time.Now()).Delete(&database.Session{})
	if result.Error != nil {
		log.Printf("Error cleaning expired sessions: %v", result.Error)
		return
	}
	log.Printf("Cleaned %d expired sessions", result.RowsAffected)
}

// archiveOldMessages archives messages older than a certain threshold
func (s *SchedulerService) archiveOldMessages() {
	// Example implementation - you would customize this based on your needs
	// This might involve moving messages to an archive table or marking them as archived
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)

	var oldMessages []database.Message
	result := s.DB.Where("created_at < ?", thirtyDaysAgo).Find(&oldMessages)
	if result.Error != nil {
		log.Printf("Error finding old messages: %v", result.Error)
		return
	}

	log.Printf("Found %d messages to archive", len(oldMessages))
	// Implement your archiving logic here
}

// syncNetworks synchronizes network data
func (s *SchedulerService) syncNetworks() {
	var networks []database.Network
	result := s.DB.Find(&networks)
	if result.Error != nil {
		log.Printf("Error finding networks: %v", result.Error)
		return
	}

	for _, network := range networks {
		log.Printf("Syncing network: %s", network.NetworkName)
		// Implement your network sync logic here
	}
}

// AddTask adds a new task to the scheduler dynamically
func (s *SchedulerService) AddTask(task Task) error {
	// Check if a task with this name already exists
	if _, exists := s.registeredTasks[task.Name]; exists {
		return fmt.Errorf("task with name '%s' already exists", task.Name)
	}

	// Register the task with the scheduler
	s.registerTask(task)

	return nil
}

// RemoveTask removes a task from the scheduler by name
func (s *SchedulerService) RemoveTask(taskName string) error {
	// Check if the task exists
	if _, exists := s.registeredTasks[taskName]; !exists {
		return fmt.Errorf("task with name '%s' does not exist", taskName)
	}

	// Remove the task from our registry
	delete(s.registeredTasks, taskName)

	// Remove the job from the scheduler
	s.scheduler.RemoveByTag(taskName)

	log.Printf("Removed task: %s", taskName)
	return nil
}
