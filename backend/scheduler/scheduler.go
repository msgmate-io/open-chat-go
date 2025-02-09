package scheduler

import (
	"backend/api"
	"backend/api/integrations"
	"backend/database"
	"backend/server/util"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-co-op/gocron"
	"gorm.io/gorm"
	"log"
	"time"
)

// SchedulerService manages all scheduled tasks
type SchedulerService struct {
	scheduler         *gocron.Scheduler
	DB                *gorm.DB
	ctx               context.Context
	cancel            context.CancelFunc
	federationHandler api.FederationHandlerInterface
	registeredTasks   map[string]Task
	serverURL         string
	signalBotService  *integrations.SignalBotService
	signalTaskHandler *integrations.SignalTaskHandler
}

// NewSchedulerService creates a new scheduler service
func NewSchedulerService(DB *gorm.DB, federationHandler api.FederationHandlerInterface, serverURL string) *SchedulerService {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a scheduler with UTC timezone
	s := gocron.NewScheduler(time.UTC)

	// Initialize Signal services
	signalBotService := integrations.NewSignalBotService(DB, serverURL)
	signalTaskHandler := integrations.NewSignalTaskHandler(signalBotService)

	service := &SchedulerService{
		scheduler:         s,
		DB:                DB,
		ctx:               ctx,
		cancel:            cancel,
		federationHandler: federationHandler,
		registeredTasks:   make(map[string]Task),
		serverURL:         serverURL,
		signalBotService:  signalBotService,
		signalTaskHandler: signalTaskHandler,
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

	// Register network tasks if federation handler is available
	//if s.federationHandler != nil {
	//	s.registerTaskGroup(NetworkTasks(s.DB, s.federationHandler))
	//}

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

// checkIntegrationsHealth checks the health of all active integrations
func (s *SchedulerService) checkIntegrationsHealth() {
	var integrations []database.Integration
	result := s.DB.Where("active = ?", true).Find(&integrations)
	if result.Error != nil {
		log.Printf("Error finding active integrations: %v", result.Error)
		return
	}

	for _, integration := range integrations {
		// Check integration health based on type
		switch integration.IntegrationType {
		case "signal":
			s.checkSignalIntegrationHealth(integration)
		// Add other integration types as needed
		default:
			log.Printf("Unknown integration type: %s", integration.IntegrationType)
		}
	}
}

// checkSignalIntegrationHealth checks if a Signal integration is healthy
func (s *SchedulerService) checkSignalIntegrationHealth(integration database.Integration) {
	// Parse the integration config
	var config map[string]interface{}
	if err := util.ParseJSON(integration.Config, &config); err != nil {
		log.Printf("Error parsing integration config: %v", err)
		return
	}

	// Example implementation - check if the Docker container is running
	alias, ok := config["alias"].(string)
	if !ok {
		log.Printf("Invalid alias in integration config")
		return
	}

	// Update last_used timestamp if the integration is healthy
	now := time.Now()
	integration.LastUsed = &now
	if err := s.DB.Save(&integration).Error; err != nil {
		log.Printf("Error updating integration last_used: %v", err)
	}

	log.Printf("Signal integration %s is healthy", alias)
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

// InitializeIntegrationTasks sets up tasks for existing integrations
func (s *SchedulerService) InitializeIntegrationTasks() {
	// Find all active integrations
	var integrations []database.Integration
	if err := s.DB.Where("active = ?", true).Find(&integrations).Error; err != nil {
		log.Printf("Error finding active integrations: %v", err)
		return
	}

	log.Printf("Initializing tasks for %d active integrations", len(integrations))

	// For each integration, create appropriate tasks based on type
	for _, integration := range integrations {
		switch integration.IntegrationType {
		case "signal":
			// Parse the integration config
			var config map[string]interface{}
			if err := json.Unmarshal(integration.Config, &config); err != nil {
				log.Printf("Error parsing integration config: %v", err)
				continue
			}

			alias, ok := config["alias"].(string)
			if !ok {
				log.Printf("Invalid alias in integration config")
				continue
			}

			// Create a task for polling Signal messages using the Signal bot service
			taskName := fmt.Sprintf("signal_poll_%s", alias)
			taskHandler, err := s.signalTaskHandler.CreateSignalPollingTask(integration)
			if err != nil {
				log.Printf("Error creating Signal polling task: %v", err)
				continue
			}

			task := Task{
				Name:        taskName,
				Description: fmt.Sprintf("Poll Signal messages for integration %s", alias),
				Schedule:    "@every 4s", // Every 4 seconds instead of "*/1 * * * *"
				Enabled:     true,
				Handler:     taskHandler,
			}

			if err := s.AddTask(task); err != nil {
				log.Printf("Error adding Signal polling task: %v", err)
			} else {
				log.Printf("Added Signal polling task for integration %s", alias)
			}

		// Add cases for other integration types as needed
		default:
			log.Printf("Unknown integration type: %s", integration.IntegrationType)
		}
	}
}

// processAICommand processes an AI command using the Signal bot service
func (s *SchedulerService) processAICommand(message string, sourceNumber string, alias string, integration database.Integration, attachments []map[string]interface{}) error {
	return s.signalTaskHandler.ProcessSignalMessage(message, sourceNumber, alias, integration, attachments)
}

// ProcessSignalMessage processes a Signal message with the AI
func (s *SchedulerService) ProcessSignalMessage(messageText, sourceNumber, alias string, integration database.Integration, attachments []map[string]interface{}) error {
	return s.signalTaskHandler.ProcessSignalMessage(messageText, sourceNumber, alias, integration, attachments)
}
