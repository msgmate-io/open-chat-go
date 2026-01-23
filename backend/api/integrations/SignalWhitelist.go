package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
)

type SignalWhitelistRequest struct {
	PhoneNumber string `json:"phone_number"`
}

// GetSignalWhitelist returns the whitelist of phone numbers for a Signal integration
//
//	@Summary      Get Signal whitelist
//	@Description  Retrieve the list of whitelisted phone numbers for a Signal integration. Only whitelisted numbers can send messages through the integration.
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        alias path string true "Signal integration alias"
//	@Success      200 {object} map[string]interface{} "Whitelist of phone numbers"
//	@Failure      400 {string} string "Unable to get database or user, or alias missing"
//	@Failure      404 {string} string "Integration not found"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/integrations/signal/{alias}/whitelist [get]
func (h *IntegrationsHandler) GetSignalWhitelist(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	alias := r.PathValue("alias")
	if alias == "" {
		http.Error(w, "Alias is required", http.StatusBadRequest)
		return
	}
	var integration database.Integration
	result := DB.Where("integration_name = ? AND integration_type = ? AND user_id = ?", alias, "signal", user.ID).First(&integration)
	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Integration with alias '%s' not found", alias), http.StatusNotFound)
		return
	}
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		http.Error(w, "Failed to parse integration configuration", http.StatusInternalServerError)
		return
	}
	var whitelist []string
	if wl, ok := config["whitelist"].([]interface{}); ok {
		for _, item := range wl {
			if str, ok := item.(string); ok {
				whitelist = append(whitelist, str)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"whitelist": whitelist,
	})
}

// AddToSignalWhitelist adds a phone number to the Signal integration whitelist
//
//	@Summary      Add to Signal whitelist
//	@Description  Add a phone number to the whitelist of a Signal integration. Whitelisted numbers are allowed to send messages through the integration.
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        alias path string true "Signal integration alias"
//	@Param        request body SignalWhitelistRequest true "Phone number to add"
//	@Success      200 {object} map[string]interface{} "Success message"
//	@Failure      400 {string} string "Unable to get database or user, alias missing, or invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/integrations/signal/{alias}/whitelist/add [post]
func (h *IntegrationsHandler) AddToSignalWhitelist(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	alias := r.PathValue("alias")
	if alias == "" {
		http.Error(w, "Alias is required", http.StatusBadRequest)
		return
	}
	var req SignalWhitelistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.PhoneNumber == "" {
		http.Error(w, "Phone number is required", http.StatusBadRequest)
		return
	}
	var integration database.Integration
	result := DB.Where("integration_name = ? AND integration_type = ? AND user_id = ?", alias, "signal", user.ID).First(&integration)
	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Integration with alias '%s' not found", alias), http.StatusNotFound)
		return
	}
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		http.Error(w, "Failed to parse integration configuration", http.StatusInternalServerError)
		return
	}
	var whitelist []string
	if wl, ok := config["whitelist"].([]interface{}); ok {
		for _, item := range wl {
			if str, ok := item.(string); ok {
				whitelist = append(whitelist, str)
			}
		}
	}
	// Add if not present
	for _, n := range whitelist {
		if n == req.PhoneNumber {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"message": "Number already in whitelist"})
			return
		}
	}
	whitelist = append(whitelist, req.PhoneNumber)
	config["whitelist"] = whitelist
	configBytes, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "Failed to serialize config", http.StatusInternalServerError)
		return
	}
	integration.Config = configBytes
	if err := DB.Save(&integration).Error; err != nil {
		http.Error(w, "Failed to update integration", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Number added to whitelist"})
}

// RemoveFromSignalWhitelist removes a phone number from the Signal integration whitelist
//
//	@Summary      Remove from Signal whitelist
//	@Description  Remove a phone number from the whitelist of a Signal integration. The number will no longer be able to send messages through the integration.
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        alias path string true "Signal integration alias"
//	@Param        request body SignalWhitelistRequest true "Phone number to remove"
//	@Success      200 {object} map[string]interface{} "Success message"
//	@Failure      400 {string} string "Unable to get database or user, alias missing, or invalid request"
//	@Failure      404 {string} string "Integration not found"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/integrations/signal/{alias}/whitelist/remove [post]
func (h *IntegrationsHandler) RemoveFromSignalWhitelist(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	alias := r.PathValue("alias")
	if alias == "" {
		http.Error(w, "Alias is required", http.StatusBadRequest)
		return
	}
	var req SignalWhitelistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.PhoneNumber == "" {
		http.Error(w, "Phone number is required", http.StatusBadRequest)
		return
	}
	var integration database.Integration
	result := DB.Where("integration_name = ? AND integration_type = ? AND user_id = ?", alias, "signal", user.ID).First(&integration)
	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Integration with alias '%s' not found", alias), http.StatusNotFound)
		return
	}
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		http.Error(w, "Failed to parse integration configuration", http.StatusInternalServerError)
		return
	}
	var whitelist []string
	if wl, ok := config["whitelist"].([]interface{}); ok {
		for _, item := range wl {
			if str, ok := item.(string); ok {
				whitelist = append(whitelist, str)
			}
		}
	}
	// Remove the number
	newWhitelist := []string{}
	removed := false
	for _, n := range whitelist {
		if n == req.PhoneNumber {
			removed = true
			continue
		}
		newWhitelist = append(newWhitelist, n)
	}
	if !removed {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"message": "Number not in whitelist"})
		return
	}
	config["whitelist"] = newWhitelist
	configBytes, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "Failed to serialize config", http.StatusInternalServerError)
		return
	}
	integration.Config = configBytes
	if err := DB.Save(&integration).Error; err != nil {
		http.Error(w, "Failed to update integration", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Number removed from whitelist"})
}
