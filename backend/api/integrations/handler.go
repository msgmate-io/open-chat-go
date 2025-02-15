package integrations

// IntegrationsHandler handles integration-related API requests
type IntegrationsHandler struct {
	SignalService *SignalIntegrationService
	MatrixService *MatrixIntegrationService
}
