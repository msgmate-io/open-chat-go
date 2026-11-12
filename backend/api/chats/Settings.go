package chats

import (
	"encoding/json"
	"net/http"
	"strings"

	"backend/database"
	"backend/server/util"

	"gorm.io/gorm"
)

// ChatInfoBit is one per-interaction "information bit" (ci#1027): a compact,
// read-only label/value pair an integration attaches to an interaction so the
// shared chat UI can render it underneath the integration widget (eg the
// triggering GitHub issue of a coding chat). Bits are user-facing only; they are
// never forwarded to the chat backend as shared config.
type ChatInfoBit struct {
	Label string `json:"label"`
	// Value is the displayed text; it defaults to the href when omitted.
	Value string `json:"value,omitempty"`
	// Href makes the bit a link. Only http(s), mailto and site-relative targets
	// survive normalization, so a stored bit can never inject a script URL.
	Href string `json:"href,omitempty"`
}

// ChatSettingsView is the user-visible projection of a chat's server-side
// settings: the optional chat title used to relabel a chat (e.g. by the
// internal_automations integration) plus the optional information bits the
// widget renders below the chat header (ci#1027).
type ChatSettingsView struct {
	Title    string        `json:"title,omitempty"`
	InfoBits []ChatInfoBit `json:"info_bits,omitempty"`
}

// Limits keep a chat's info-bit strip compact on every breakpoint and stop a
// misbehaving integration from turning the widget into a wall of text.
const (
	maxChatInfoBits       = 12
	maxChatInfoBitLabel   = 64
	maxChatInfoBitValue   = 200
	maxChatInfoBitHrefLen = 2048
)

type updateChatSettingsRequest struct {
	Title *string `json:"title"`
	// Tags replaces the chat's tag set when provided. Send an empty array to
	// clear all tags. Nil leaves the existing tags untouched.
	Tags *[]string `json:"tags"`
	// InfoBits replaces the chat's information bits when provided. Send an empty
	// array to clear them. Nil leaves the existing bits untouched.
	InfoBits *[]ChatInfoBit `json:"info_bits"`
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
	bits := decodeChatInfoBits(config["info_bits"])
	if title == "" && len(bits) == 0 {
		return nil
	}
	return &ChatSettingsView{Title: title, InfoBits: bits}
}

// decodeChatInfoBits re-reads the stored `info_bits` array. The value round-trips
// through the generic config map, so it is normalized again on read: a hand
// edited or older row still renders the same way.
func decodeChatInfoBits(raw interface{}) []ChatInfoBit {
	if raw == nil {
		return nil
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var bits []ChatInfoBit
	if err := json.Unmarshal(blob, &bits); err != nil {
		return nil
	}
	return normalizeChatInfoBits(bits)
}

// normalizeChatInfoBits trims, caps and filters info bits: a bit without a label
// is dropped (it could not be rendered), a bit without any text falls back to its
// link target, and unsafe link schemes are stripped so the bit renders as plain
// text instead of a link.
func normalizeChatInfoBits(bits []ChatInfoBit) []ChatInfoBit {
	out := make([]ChatInfoBit, 0, len(bits))
	for _, bit := range bits {
		label := trimToLen(bit.Label, maxChatInfoBitLabel)
		if label == "" {
			continue
		}
		href := sanitizeInfoBitHref(bit.Href)
		value := trimToLen(bit.Value, maxChatInfoBitValue)
		if value == "" {
			value = href
		}
		if value == "" {
			continue
		}
		out = append(out, ChatInfoBit{Label: label, Value: value, Href: href})
		if len(out) >= maxChatInfoBits {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeInfoBitHref keeps a link target only when it is safe to render:
// http(s), mailto or a site-relative path. Anything else (eg javascript:) is
// dropped and the bit degrades to plain text.
func sanitizeInfoBitHref(raw string) string {
	href := trimToLen(raw, maxChatInfoBitHrefLen)
	if href == "" {
		return ""
	}
	lower := strings.ToLower(href)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") {
		return href
	}
	if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		return href
	}
	return ""
}

// trimToLen trims whitespace and truncates to at most limit runes.
func trimToLen(raw string, limit int) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) > limit {
		trimmed = strings.TrimSpace(string(runes[:limit]))
	}
	return trimmed
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

	chatUUID, chatUUIDErr := parseChatUUID(r)
	if chatUUIDErr != nil {
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
	if payload.InfoBits != nil {
		// Info bits replace the whole strip: an empty array clears it, so an
		// integration that no longer knows anything about the interaction does not
		// leave stale labels behind (ci#1027).
		if bits := normalizeChatInfoBits(*payload.InfoBits); len(bits) > 0 {
			config["info_bits"] = bits
		} else {
			delete(config, "info_bits")
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
	} else if err := DB.Model(&settings).Update("config_data", database.JSONRaw(encoded)).Error; err != nil {
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
