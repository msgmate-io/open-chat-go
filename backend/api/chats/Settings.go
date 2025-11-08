package chats

import (
	"encoding/json"
	"net/http"
	"strings"

	"backend/database"
	"backend/server/util"

	"gorm.io/gorm"
)

// ChatSettingsView is the user-visible projection of a chat's server-side
// settings. Today it only carries the optional chat title used to relabel a
// chat (e.g. by the internal_automations integration).
type ChatSettingsView struct {
	Title string `json:"title,omitempty"`
}

type updateChatSettingsRequest struct {
	Title *string `json:"title"`
	// Tags replaces the chat's tag set when provided. Send an empty array to
	// clear all tags. Nil leaves the existing tags untouched.
	Tags *[]string `json:"tags"`
}

func chatSettingsViewFromRaw(raw json.RawMessage) *ChatSettingsView {
	if len(raw) == 0 {
		return nil
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil
	}
	title, _ := config["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	return &ChatSettingsView{Title: title}
}

// loadChatSettingsViews batch-loads the visible settings for the given chats.
func loadChatSettingsViews(db *gorm.DB, chatIDs []uint) map[uint]*ChatSettingsView {
	out := map[uint]*ChatSettingsView{}
	if len(chatIDs) == 0 {
		return out
	}
	rows := []database.ChatSettings{}
	if err := db.Where("chat_id IN ?", chatIDs).Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		if view := chatSettingsViewFromRaw(row.ConfigData); view != nil {
			out[row.ChatId] = view
		}
	}
	return out
}

// UpdateSettings sets or clears the server-side settings (currently the title)
// of a chat owned by the authenticated user.
//
//	@Summary      Update chat settings
//	@Description  Set or clear server-side chat settings (currently the chat title)
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        request body chats.updateChatSettingsRequest true "Chat settings update"
//	@Success      200 {object} map[string]interface{} "Updated chat settings"
//	@Failure      400 {string} string "Invalid chat UUID or JSON"
//	@Router       /api/v1/chats/{chat_uuid}/settings [put]
func (h *ChatsHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := strings.TrimSpace(r.PathValue("chat_uuid"))
	if chatUUID == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	var chat database.Chat
	result := DB.Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, user.ID, user.ID).First(&chat)
	if result.Error != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	var payload updateChatSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	settings := database.ChatSettings{}
	findErr := DB.Where("chat_id = ?", chat.ID).First(&settings).Error
	if findErr != nil && findErr != gorm.ErrRecordNotFound {
		http.Error(w, "Unable to load chat settings", http.StatusInternalServerError)
		return
	}

	config := map[string]interface{}{}
	if findErr == nil && len(settings.ConfigData) > 0 {
		_ = json.Unmarshal(settings.ConfigData, &config)
	}
	if payload.Title != nil {
		title := strings.TrimSpace(*payload.Title)
		if title == "" {
			delete(config, "title")
		} else {
			config["title"] = title
		}
	}
	if payload.Tags != nil {
		if err := DB.Model(&chat).Update("tags", database.NormalizeChatTags(*payload.Tags)).Error; err != nil {
			http.Error(w, "Unable to save chat tags", http.StatusInternalServerError)
			return
		}
		chat.Tags = database.NormalizeChatTags(*payload.Tags)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "Unable to encode chat settings", http.StatusInternalServerError)
		return
	}

	if findErr == gorm.ErrRecordNotFound {
		if err := DB.Create(&database.ChatSettings{ChatId: chat.ID, ConfigData: encoded}).Error; err != nil {
			http.Error(w, "Unable to save chat settings", http.StatusInternalServerError)
			return
		}
	} else if err := DB.Model(&settings).Update("config_data", encoded).Error; err != nil {
		http.Error(w, "Unable to save chat settings", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	tags := []string(chat.Tags)
	if tags == nil {
		tags = []string{}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"chat_uuid": chat.UUID,
		"settings":  chatSettingsViewFromRaw(encoded),
		"tags":      tags,
	})
}
