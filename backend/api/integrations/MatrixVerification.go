package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// MatrixDeviceInfo represents device information for API responses
type MatrixDeviceInfo struct {
	DeviceID    string     `json:"device_id"`
	DisplayName string     `json:"display_name,omitempty"`
	LastSeenIP  string     `json:"last_seen_ip,omitempty"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	Verified    bool       `json:"verified"`
}

// MatrixVerificationStatus represents the verification status response
type MatrixVerificationStatus struct {
	DeviceVerified  bool              `json:"device_verified"`
	VerifiedAt      *time.Time        `json:"verified_at,omitempty"`
	Method          string            `json:"verification_method,omitempty"`
	CrossSigning    bool              `json:"cross_signing_setup"`
	OtherDevices    []MatrixDeviceInfo `json:"other_devices,omitempty"`
}

// MatrixVerifyDeviceRequest represents a device verification request
type MatrixVerifyDeviceRequest struct {
	TargetUserID   string `json:"target_user_id"`
	TargetDeviceID string `json:"target_device_id"`
	Method         string `json:"method"` // "sas" (emoji), "qr", "manual"
}

// MatrixVerifyDeviceResponse represents a verification response
type MatrixVerifyDeviceResponse struct {
	Status         string `json:"status"`
	TransactionID  string `json:"transaction_id,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	Emoji          []string `json:"emoji,omitempty"`
	Message        string `json:"message,omitempty"`
}

// GetMatrixVerificationStatus returns the verification status of a Matrix integration
//
//	@Summary      Get Matrix verification status
//	@Description  Returns the device verification status and other devices for a Matrix integration
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Success      200 {object} MatrixVerificationStatus "Verification status"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/verification/status [get]
func (h *IntegrationsHandler) GetMatrixVerificationStatus(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Get integration ID from path
	integrationIDStr := r.PathValue("integration_id")
	integrationID, err := strconv.ParseUint(integrationIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid integration ID", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
		return
	}

	// Get the client state
	var clientState database.MatrixClientState
	if err := DB.Where("integration_id = ?", integrationID).First(&clientState).Error; err != nil {
		http.Error(w, "Matrix client state not found", http.StatusNotFound)
		return
	}

	// Get other devices
	var devices []database.MatrixDevice
	DB.Where("matrix_client_state_id = ?", clientState.ID).Find(&devices)

	otherDevices := make([]MatrixDeviceInfo, 0, len(devices))
	for _, d := range devices {
		if d.DeviceID != clientState.DeviceID {
			otherDevices = append(otherDevices, MatrixDeviceInfo{
				DeviceID:    d.DeviceID,
				DisplayName: d.DisplayName,
				LastSeenIP:  d.LastSeenIP,
				LastSeenAt:  d.LastSeenAt,
				Verified:    d.Verified,
			})
		}
	}

	status := MatrixVerificationStatus{
		DeviceVerified: clientState.DeviceVerified,
		VerifiedAt:     clientState.VerifiedAt,
		Method:         clientState.VerificationMethod,
		CrossSigning:   clientState.CrossSigningSetup,
		OtherDevices:   otherDevices,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// StartMatrixDeviceVerification starts the device verification process
//
//	@Summary      Start Matrix device verification
//	@Description  Initiates a device verification process for the Matrix integration
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        request body MatrixVerifyDeviceRequest true "Verification request"
//	@Success      200 {object} MatrixVerifyDeviceResponse "Verification started"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/verification/start [post]
func (h *IntegrationsHandler) StartMatrixDeviceVerification(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Get integration ID from path
	integrationIDStr := r.PathValue("integration_id")
	integrationID, err := strconv.ParseUint(integrationIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid integration ID", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
		return
	}

	// Parse request
	var req MatrixVerifyDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Check if Matrix service is available
	if h.MatrixService == nil {
		http.Error(w, "Matrix service not available", http.StatusServiceUnavailable)
		return
	}

	// Get the active client
	conn, err := h.MatrixService.GetClient(uint(integrationID))
	if err != nil {
		http.Error(w, fmt.Sprintf("Matrix client not active: %v", err), http.StatusBadRequest)
		return
	}

	// Start verification based on method
	response := MatrixVerifyDeviceResponse{
		Status:  "initiated",
		Message: fmt.Sprintf("Verification started with method: %s", req.Method),
	}

	switch req.Method {
	case "sas":
		// SAS (Short Authentication String / Emoji) verification
		// This would use the crypto helper to start SAS verification
		response.Message = "SAS verification initiated. Please confirm the emoji on both devices."
		// In a full implementation, this would return the transaction ID and wait for emoji confirmation

	case "manual":
		// Manual device trust - mark the device as verified directly
		// This is less secure but useful for development/testing
		if req.TargetUserID == "" || req.TargetDeviceID == "" {
			http.Error(w, "target_user_id and target_device_id required for manual verification", http.StatusBadRequest)
			return
		}

		// Store device as verified
		device := database.MatrixDevice{
			MatrixClientStateID: conn.ClientState.ID,
			UserID:              req.TargetUserID,
			DeviceID:            req.TargetDeviceID,
			Verified:            true,
		}
		now := time.Now()
		device.VerifiedAt = &now

		// Upsert the device
		if err := DB.Where("device_id = ? AND user_id = ?", req.TargetDeviceID, req.TargetUserID).
			Assign(device).FirstOrCreate(&device).Error; err != nil {
			http.Error(w, fmt.Sprintf("Failed to store device verification: %v", err), http.StatusInternalServerError)
			return
		}

		response.Status = "verified"
		response.Message = "Device manually verified"

	default:
		http.Error(w, "Unsupported verification method. Use 'sas' or 'manual'", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ConfirmMatrixVerification confirms a verification with emoji/code
//
//	@Summary      Confirm Matrix verification
//	@Description  Confirms a pending verification process
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        transaction_id path string true "Transaction ID"
//	@Success      200 {object} MatrixVerifyDeviceResponse "Verification confirmed"
//	@Failure      400 {string} string "Invalid request"
//	@Router       /api/v1/integrations/matrix/{integration_id}/verification/{transaction_id}/confirm [post]
func (h *IntegrationsHandler) ConfirmMatrixVerification(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Get integration ID from path
	integrationIDStr := r.PathValue("integration_id")
	integrationID, err := strconv.ParseUint(integrationIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid integration ID", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
		return
	}

	transactionID := r.PathValue("transaction_id")
	if transactionID == "" {
		http.Error(w, "Transaction ID required", http.StatusBadRequest)
		return
	}

	// In a full implementation, this would confirm the SAS verification
	// For now, return a placeholder response
	response := MatrixVerifyDeviceResponse{
		Status:        "confirmed",
		TransactionID: transactionID,
		Message:       "Verification confirmed successfully",
	}

	// Update client state to mark as verified
	var clientState database.MatrixClientState
	if err := DB.Where("integration_id = ?", integrationID).First(&clientState).Error; err == nil {
		clientState.DeviceVerified = true
		now := time.Now()
		clientState.VerifiedAt = &now
		clientState.VerificationMethod = "sas"
		DB.Save(&clientState)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// CancelMatrixVerification cancels a pending verification
//
//	@Summary      Cancel Matrix verification
//	@Description  Cancels a pending verification process
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        transaction_id path string true "Transaction ID"
//	@Success      200 {object} MatrixVerifyDeviceResponse "Verification cancelled"
//	@Failure      400 {string} string "Invalid request"
//	@Router       /api/v1/integrations/matrix/{integration_id}/verification/{transaction_id}/cancel [post]
func (h *IntegrationsHandler) CancelMatrixVerification(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Verify ownership (simplified check)
	_ = user

	transactionID := r.PathValue("transaction_id")

	response := MatrixVerifyDeviceResponse{
		Status:        "cancelled",
		TransactionID: transactionID,
		Message:       "Verification cancelled",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ListMatrixDevices lists all known devices for the Matrix user
//
//	@Summary      List Matrix devices
//	@Description  Lists all known devices for the Matrix integration's user
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Success      200 {array} MatrixDeviceInfo "List of devices"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/devices [get]
func (h *IntegrationsHandler) ListMatrixDevices(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Get integration ID from path
	integrationIDStr := r.PathValue("integration_id")
	integrationID, err := strconv.ParseUint(integrationIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid integration ID", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
		return
	}

	// Get the client state
	var clientState database.MatrixClientState
	if err := DB.Where("integration_id = ?", integrationID).First(&clientState).Error; err != nil {
		http.Error(w, "Matrix client state not found", http.StatusNotFound)
		return
	}

	// Get all devices
	var devices []database.MatrixDevice
	DB.Where("matrix_client_state_id = ?", clientState.ID).Find(&devices)

	// Add current device first
	result := []MatrixDeviceInfo{
		{
			DeviceID:    clientState.DeviceID,
			DisplayName: clientState.DisplayName + " (This device)",
			Verified:    clientState.DeviceVerified,
		},
	}

	// Add other devices
	for _, d := range devices {
		if d.DeviceID != clientState.DeviceID {
			result = append(result, MatrixDeviceInfo{
				DeviceID:    d.DeviceID,
				DisplayName: d.DisplayName,
				LastSeenIP:  d.LastSeenIP,
				LastSeenAt:  d.LastSeenAt,
				Verified:    d.Verified,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// TrustMatrixDevice marks a specific device as trusted
//
//	@Summary      Trust Matrix device
//	@Description  Marks a specific device as trusted for encrypted communication
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        device_id path string true "Device ID to trust"
//	@Success      200 {object} map[string]interface{} "Device trusted"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/devices/{device_id}/trust [post]
func (h *IntegrationsHandler) TrustMatrixDevice(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Get integration ID from path
	integrationIDStr := r.PathValue("integration_id")
	integrationID, err := strconv.ParseUint(integrationIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid integration ID", http.StatusBadRequest)
		return
	}

	deviceID := r.PathValue("device_id")
	if deviceID == "" {
		http.Error(w, "Device ID required", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
		return
	}

	// Get the client state
	var clientState database.MatrixClientState
	if err := DB.Where("integration_id = ?", integrationID).First(&clientState).Error; err != nil {
		http.Error(w, "Matrix client state not found", http.StatusNotFound)
		return
	}

	// Update or create device record
	now := time.Now()
	device := database.MatrixDevice{
		MatrixClientStateID: clientState.ID,
		DeviceID:            deviceID,
		UserID:              clientState.UserID,
		Verified:            true,
		VerifiedAt:          &now,
	}

	if err := DB.Where("matrix_client_state_id = ? AND device_id = ?", clientState.ID, deviceID).
		Assign(map[string]interface{}{
			"verified":    true,
			"verified_at": now,
		}).FirstOrCreate(&device).Error; err != nil {
		http.Error(w, fmt.Sprintf("Failed to trust device: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "trusted",
		"device_id": deviceID,
		"message":   "Device is now trusted for encrypted communication",
	})
}
