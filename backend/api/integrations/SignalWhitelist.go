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

// List whitelist numbers for a Signal integration
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

// Add a number to the Signal integration whitelist
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

// Remove a number from the Signal integration whitelist
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
