package integrations

import "backend/scheduler"

// IntegrationsHandler handles integration-related API requests
type IntegrationsHandler struct {
	SchedulerService *scheduler.SchedulerService
	SignalService    *SignalIntegrationService
}
