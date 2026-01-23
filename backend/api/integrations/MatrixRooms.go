package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// MatrixRoomInfo represents room information for API responses
type MatrixRoomInfo struct {
	RoomID      string `json:"room_id"`
	RoomAlias   string `json:"room_alias,omitempty"`
	Name        string `json:"name,omitempty"`
	Topic       string `json:"topic,omitempty"`
	IsEncrypted bool   `json:"is_encrypted"`
	IsDirect    bool   `json:"is_direct"`
	ChatID      *uint  `json:"chat_id,omitempty"`
}

// SendMatrixMessageRequest represents a message send request
type SendMatrixMessageRequest struct {
	RoomID  string `json:"room_id"`
	Message string `json:"message"`
	MsgType string `json:"msg_type,omitempty"` // "text", "notice", "emote"
}

// SendMatrixMessageResponse represents a message send response
type SendMatrixMessageResponse struct {
	EventID string `json:"event_id"`
	RoomID  string `json:"room_id"`
	Message string `json:"message"`
}

// SendMatrixMessage sends a message to a Matrix room
//
//	@Summary      Send Matrix message
//	@Description  Sends a message to a Matrix room (will be encrypted if room is encrypted)
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        request body SendMatrixMessageRequest true "Message to send"
//	@Success      200 {object} SendMatrixMessageResponse "Message sent"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/send [post]
func (h *IntegrationsHandler) SendMatrixMessage(w http.ResponseWriter, r *http.Request) {
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
	var req SendMatrixMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.RoomID == "" {
		http.Error(w, "room_id is required", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	// Check if Matrix service is available
	if h.MatrixService == nil {
		http.Error(w, "Matrix service not available", http.StatusServiceUnavailable)
		return
	}

	// Send the message
	if err := h.MatrixService.SendMessage(uint(integrationID), req.RoomID, req.Message); err != nil {
		http.Error(w, fmt.Sprintf("Failed to send message: %v", err), http.StatusInternalServerError)
		return
	}

	response := SendMatrixMessageResponse{
		RoomID:  req.RoomID,
		Message: "Message sent successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ListMatrixRooms lists all rooms for the Matrix integration
//
//	@Summary      List Matrix rooms
//	@Description  Lists all rooms the Matrix bot has joined
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Success      200 {array} MatrixRoomInfo "List of rooms"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/rooms [get]
func (h *IntegrationsHandler) ListMatrixRooms(w http.ResponseWriter, r *http.Request) {
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

	// Get all rooms
	var rooms []database.MatrixRoom
	DB.Where("matrix_client_state_id = ?", clientState.ID).Find(&rooms)

	result := make([]MatrixRoomInfo, 0, len(rooms))
	for _, room := range rooms {
		result = append(result, MatrixRoomInfo{
			RoomID:      room.RoomID,
			RoomAlias:   room.RoomAlias,
			Name:        room.Name,
			Topic:       room.Topic,
			IsEncrypted: room.IsEncrypted,
			IsDirect:    room.IsDirect,
			ChatID:      room.ChatID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// JoinMatrixRoomRequest represents a room join request
type JoinMatrixRoomRequest struct {
	RoomIDOrAlias string `json:"room_id_or_alias"`
}

// JoinMatrixRoom joins a Matrix room
//
//	@Summary      Join Matrix room
//	@Description  Joins a Matrix room by ID or alias
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        room_id path string true "Room ID or alias to join"
//	@Success      200 {object} map[string]interface{} "Room joined"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/rooms/{room_id}/join [post]
func (h *IntegrationsHandler) JoinMatrixRoom(w http.ResponseWriter, r *http.Request) {
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

	roomID := r.PathValue("room_id")
	if roomID == "" {
		http.Error(w, "Room ID is required", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
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

	// Join the room
	resp, err := conn.Client.JoinRoom(conn.ctx, roomID, "", nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to join room: %v", err), http.StatusInternalServerError)
		return
	}

	// Get client state for storing room
	var clientState database.MatrixClientState
	if err := DB.Where("integration_id = ?", integrationID).First(&clientState).Error; err == nil {
		// Create or update room record
		room := database.MatrixRoom{
			MatrixClientStateID: clientState.ID,
			RoomID:              string(resp.RoomID),
		}
		DB.Where("room_id = ? AND matrix_client_state_id = ?", string(resp.RoomID), clientState.ID).
			FirstOrCreate(&room)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "joined",
		"room_id": string(resp.RoomID),
		"message": "Successfully joined room",
	})
}

// GetMatrixWhitelist returns the whitelist for a Matrix integration
//
//	@Summary      Get Matrix whitelist
//	@Description  Returns the list of whitelisted Matrix users/rooms for AI processing
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Success      200 {object} map[string]interface{} "Whitelist"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/whitelist [get]
func (h *IntegrationsHandler) GetMatrixWhitelist(w http.ResponseWriter, r *http.Request) {
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

	// Parse config to get whitelist
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		http.Error(w, "Invalid integration config", http.StatusInternalServerError)
		return
	}

	whitelist, _ := config["whitelist"].([]interface{})
	if whitelist == nil {
		whitelist = []interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"whitelist": whitelist,
	})
}

// AddToMatrixWhitelistRequest represents an add to whitelist request
type AddToMatrixWhitelistRequest struct {
	UserID string `json:"user_id"` // Matrix user ID to whitelist
}

// AddToMatrixWhitelist adds a user to the Matrix integration whitelist
//
//	@Summary      Add to Matrix whitelist
//	@Description  Adds a Matrix user ID to the whitelist for AI processing
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        request body AddToMatrixWhitelistRequest true "User to add"
//	@Success      200 {object} map[string]interface{} "User added"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/whitelist/add [post]
func (h *IntegrationsHandler) AddToMatrixWhitelist(w http.ResponseWriter, r *http.Request) {
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

	// Parse request
	var req AddToMatrixWhitelistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
		return
	}

	// Parse config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		http.Error(w, "Invalid integration config", http.StatusInternalServerError)
		return
	}

	// Get or create whitelist
	whitelist, _ := config["whitelist"].([]interface{})
	if whitelist == nil {
		whitelist = []interface{}{}
	}

	// Check if already in whitelist
	for _, item := range whitelist {
		if str, ok := item.(string); ok && str == req.UserID {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "already_exists",
				"message": "User already in whitelist",
			})
			return
		}
	}

	// Add to whitelist
	whitelist = append(whitelist, req.UserID)
	config["whitelist"] = whitelist

	// Save config
	configBytes, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "Failed to serialize config", http.StatusInternalServerError)
		return
	}

	integration.Config = configBytes
	if err := DB.Save(&integration).Error; err != nil {
		http.Error(w, "Failed to save integration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "added",
		"user_id": req.UserID,
		"message": "User added to whitelist",
	})
}

// RemoveFromMatrixWhitelistRequest represents a remove from whitelist request
type RemoveFromMatrixWhitelistRequest struct {
	UserID string `json:"user_id"` // Matrix user ID to remove
}

// RemoveFromMatrixWhitelist removes a user from the Matrix integration whitelist
//
//	@Summary      Remove from Matrix whitelist
//	@Description  Removes a Matrix user ID from the whitelist
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        integration_id path int true "Integration ID"
//	@Param        request body RemoveFromMatrixWhitelistRequest true "User to remove"
//	@Success      200 {object} map[string]interface{} "User removed"
//	@Failure      400 {string} string "Invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Router       /api/v1/integrations/matrix/{integration_id}/whitelist/remove [post]
func (h *IntegrationsHandler) RemoveFromMatrixWhitelist(w http.ResponseWriter, r *http.Request) {
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

	// Parse request
	var req RemoveFromMatrixWhitelistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	// Find the integration
	var integration database.Integration
	if err := DB.Where("id = ? AND user_id = ? AND integration_type = ?", integrationID, user.ID, "matrix").First(&integration).Error; err != nil {
		http.Error(w, "Matrix integration not found", http.StatusNotFound)
		return
	}

	// Parse config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		http.Error(w, "Invalid integration config", http.StatusInternalServerError)
		return
	}

	// Get whitelist
	whitelist, _ := config["whitelist"].([]interface{})
	if whitelist == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "not_found",
			"message": "User not in whitelist",
		})
		return
	}

	// Remove from whitelist
	newWhitelist := []interface{}{}
	found := false
	for _, item := range whitelist {
		if str, ok := item.(string); ok && str == req.UserID {
			found = true
			continue
		}
		newWhitelist = append(newWhitelist, item)
	}

	if !found {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "not_found",
			"message": "User not in whitelist",
		})
		return
	}

	config["whitelist"] = newWhitelist

	// Save config
	configBytes, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "Failed to serialize config", http.StatusInternalServerError)
		return
	}

	integration.Config = configBytes
	if err := DB.Save(&integration).Error; err != nil {
		http.Error(w, "Failed to save integration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "removed",
		"user_id": req.UserID,
		"message": "User removed from whitelist",
	})
}
