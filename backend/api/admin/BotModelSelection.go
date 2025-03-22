package admin

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"

	"gorm.io/gorm"
)

type botModelSelectionRequest struct {
	Action   string   `json:"action"`
	ModelIDs []string `json:"model_ids"`
}

type botModelSelectionResponse struct {
	BotUUID       string `json:"bot_uuid"`
	Action        string `json:"action"`
	AddedCount    int    `json:"added_count"`
	RemovedCount  int    `json:"removed_count"`
	CurrentModels int    `json:"current_models"`
}

func UpdateBotModelSelection(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	botIdentifier := r.PathValue("bot_uuid")
	if botIdentifier == "" {
		http.Error(w, "bot_uuid (or bot contact token) is required", http.StatusBadRequest)
		return
	}

	var req botModelSelectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Action != "add_missing" && req.Action != "remove_selected" {
		http.Error(w, "action must be add_missing or remove_selected", http.StatusBadRequest)
		return
	}

	selectedSet := map[string]struct{}{}
	for _, modelID := range req.ModelIDs {
		if modelID == "" {
			continue
		}
		selectedSet[modelID] = struct{}{}
	}

	botUser, err := resolveAutomatedBotByIdentifier(DB, botIdentifier)
	if err != nil {
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	addedCount := 0
	removedCount := 0

	for modelID := range selectedSet {
		if req.Action == "remove_selected" {
			changed, err := database.UnassignBotFromModelConfig(DB, modelID, botUser.Name)
			if err != nil {
				continue
			}
			if changed {
				removedCount++
			}
			continue
		}

		changed, err := database.AssignBotToModelConfig(DB, modelID, botUser.Name)
		if err != nil {
			continue
		}
		if changed {
			addedCount++
		}
	}

	if err := msgmate.CreateOrUpdateBotProfile(DB, botUser); err != nil {
		http.Error(w, "Failed to sync bot profile", http.StatusInternalServerError)
		return
	}

	currentModels, err := msgmate.GetBotModels(DB, botUser.Name)
	if err != nil {
		http.Error(w, "Failed to load bot models", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(botModelSelectionResponse{
		BotUUID:       botUser.UUID,
		Action:        req.Action,
		AddedCount:    addedCount,
		RemovedCount:  removedCount,
		CurrentModels: len(currentModels),
	})
}

func resolveAutomatedBotByIdentifier(DB *gorm.DB, identifier string) (database.User, error) {
	var botUser database.User
	if err := DB.Where("uuid = ? AND is_automated = ?", identifier, true).First(&botUser).Error; err == nil {
		return botUser, nil
	}

	var contact database.Contact
	if err := DB.Preload("ContactUser").Where("contact_token = ?", identifier).First(&contact).Error; err != nil {
		return database.User{}, err
	}
	if !contact.ContactUser.IsAutomated {
		return database.User{}, gorm.ErrRecordNotFound
	}
	return contact.ContactUser, nil
}
