package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// IntegrationStatus represents the status response for an integration
type IntegrationStatus struct {
	Status string      `json:"status"`
	Health string      `json:"health"`
	Data   interface{} `json:"data"`
}

// GetIntegrationStatus handles the status endpoint for integrations
// GET /api/v1/integrations/{integration_type}/{integration_alias}/status
func (h *IntegrationsHandler) GetIntegrationStatus(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Extract integration type and alias from path parameters
	integrationType := r.PathValue("integration_type")
	integrationAlias := r.PathValue("integration_alias")

	// Validate path parameters
	if integrationType == "" {
		http.Error(w, "Integration type is required", http.StatusBadRequest)
		return
	}

	if integrationAlias == "" {
		http.Error(w, "Integration alias is required", http.StatusBadRequest)
		return
	}

	// Find the integration in the database
	var integration database.Integration
	result := DB.Where("integration_name = ? AND integration_type = ? AND user_id = ?",
		integrationAlias, integrationType, user.ID).First(&integration)

	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Integration '%s' of type '%s' not found", integrationAlias, integrationType), http.StatusNotFound)
		return
	}

	// Check if integration is active
	if !integration.Active {
		http.Error(w, "Integration is not active", http.StatusBadRequest)
		return
	}

	// Parse the integration config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		http.Error(w, "Invalid integration configuration", http.StatusInternalServerError)
		return
	}

	// Handle different integration types
	var status IntegrationStatus
	switch integrationType {
	case "signal":
		status, err = h.getSignalIntegrationStatus(config, integrationAlias)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error getting Signal integration status: %v", err), http.StatusInternalServerError)
			return
		}
	default:
		// For unknown integration types, return a generic status
		status = IntegrationStatus{
			Status: "unknown",
			Health: "unknown",
			Data:   "Integration type not supported for status checking",
		}
	}

	// Return the status response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// getSignalIntegrationStatus checks the status of a Signal integration
func (h *IntegrationsHandler) getSignalIntegrationStatus(config map[string]interface{}, alias string) (IntegrationStatus, error) {
	// Extract port from config
	port, ok := config["port"].(float64)
	if !ok {
		return IntegrationStatus{}, fmt.Errorf("invalid port in integration config")
	}

	// Extract phone number from config
	phoneNumber, ok := config["phone_number"].(string)
	if !ok {
		return IntegrationStatus{}, fmt.Errorf("invalid phone_number in integration config")
	}

	// Check health endpoint
	healthURL := fmt.Sprintf("http://localhost:%d/v1/health", int(port))
	healthResp, err := http.Get(healthURL)
	if err != nil {
		return IntegrationStatus{
			Status: "error",
			Health: "unhealthy",
			Data:   fmt.Sprintf("Failed to connect to Signal REST API: %v", err),
		}, nil
	}
	defer healthResp.Body.Close()

	// Read health response
	healthBody, err := io.ReadAll(healthResp.Body)
	if err != nil {
		return IntegrationStatus{
			Status: "error",
			Health: "unhealthy",
			Data:   fmt.Sprintf("Failed to read health response: %v", err),
		}, nil
	}

	// Check if response is empty
	if len(healthBody) == 0 {
		// Empty response with 200 or 204 status is considered healthy
		if healthResp.StatusCode == http.StatusOK || healthResp.StatusCode == http.StatusNoContent {
			// Continue with account check
		} else {
			return IntegrationStatus{
				Status: "error",
				Health: "unhealthy",
				Data:   fmt.Sprintf("Health endpoint returned status %d with empty body", healthResp.StatusCode),
			}, nil
		}
	} else {
		// Parse health response if not empty
		var healthData map[string]interface{}
		if err := json.Unmarshal(healthBody, &healthData); err != nil {
			return IntegrationStatus{
				Status: "error",
				Health: "unhealthy",
				Data:   fmt.Sprintf("Failed to parse health response: %v", err),
			}, nil
		}

		// Check if health is "healthy"
		healthStatus, ok := healthData["status"].(string)
		if !ok || healthStatus != "healthy" {
			return IntegrationStatus{
				Status: "error",
				Health: healthStatus,
				Data:   "Signal REST API is not healthy",
			}, nil
		}
	}

	// Check accounts endpoint
	accountsURL := fmt.Sprintf("http://localhost:%d/v1/accounts", int(port))
	accountsResp, err := http.Get(accountsURL)
	if err != nil {
		return IntegrationStatus{
			Status: "error",
			Health: "healthy",
			Data:   fmt.Sprintf("Failed to connect to accounts endpoint: %v", err),
		}, nil
	}
	defer accountsResp.Body.Close()

	// Read accounts response
	accountsBody, err := io.ReadAll(accountsResp.Body)
	if err != nil {
		return IntegrationStatus{
			Status: "error",
			Health: "healthy",
			Data:   fmt.Sprintf("Failed to read accounts response: %v", err),
		}, nil
	}

	// Parse accounts response
	var accountsData []string
	if err := json.Unmarshal(accountsBody, &accountsData); err != nil {
		return IntegrationStatus{
			Status: "error",
			Health: "healthy",
			Data:   fmt.Sprintf("Failed to parse accounts response: %v", err),
		}, nil
	}

	// Check if the configured phone number is in the accounts list
	accountConfigured := false
	for _, account := range accountsData {
		if account == phoneNumber {
			accountConfigured = true
			break
		}
	}

	if accountConfigured {
		// Account is configured
		return IntegrationStatus{
			Status: "configured",
			Health: "healthy",
			Data:   "Account is properly configured and ready to use",
		}, nil
	} else {
		// Account is not configured, get QR code
		qrCodeURL := fmt.Sprintf("http://localhost:%d/v1/qrcodelink?device_name=%s", int(port), alias)
		qrResp, err := http.Get(qrCodeURL)
		if err != nil {
			return IntegrationStatus{
				Status: "unconfigured",
				Health: "healthy",
				Data:   fmt.Sprintf("Failed to get QR code: %v", err),
			}, nil
		}
		defer qrResp.Body.Close()

		qrBody, err := io.ReadAll(qrResp.Body)
		if err != nil {
			return IntegrationStatus{
				Status: "unconfigured",
				Health: "healthy",
				Data:   fmt.Sprintf("Failed to read QR code response: %v", err),
			}, nil
		}

		// If the response is a PNG image, base64 encode it directly
		contentType := qrResp.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "image/png") || (len(qrBody) > 4 && string(qrBody[:4]) == "\x89PNG") {
			base64QR := base64.StdEncoding.EncodeToString(qrBody)
			return IntegrationStatus{
				Status: "unconfigured",
				Health: "healthy",
				Data:   base64QR,
			}, nil
		}

		// Otherwise, try to parse as JSON (legacy/fallback)
		var qrData map[string]interface{}
		if err := json.Unmarshal(qrBody, &qrData); err == nil {
			qrImage, ok := qrData["qrcode"].(string)
			if ok {
				// Extract base64 from data URL if present
				if strings.HasPrefix(qrImage, "data:image/") {
					parts := strings.Split(qrImage, ",")
					if len(parts) == 2 {
						return IntegrationStatus{
							Status: "unconfigured",
							Health: "healthy",
							Data:   parts[1],
						}, nil
					}
				}
				// Otherwise, just return the string
				return IntegrationStatus{
					Status: "unconfigured",
					Health: "healthy",
					Data:   qrImage,
				}, nil
			}
			return IntegrationStatus{
				Status: "unconfigured",
				Health: "healthy",
				Data:   "QR code not available in response",
			}, nil
		}

		// If all else fails, return an error
		return IntegrationStatus{
			Status: "unconfigured",
			Health: "healthy",
			Data:   "QR code not available or could not be parsed",
		}, nil
	}
}
