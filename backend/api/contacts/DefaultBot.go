package contacts

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

// GetDefaultBot
// @Summary      Get the default platform bot contact
// @Description  Resolve the default platform bot (user named "bot") and return the current user's contact for it, creating the contact if it does not exist yet. Pass `?bot=<identifier>` (or the legacy `?username=`) to resolve a specific automated bot by username, name or contact token instead of the platform default.
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        bot query string false "Bot identifier (username, name or contact token)"
// @Param        username query string false "Deprecated alias for bot"
// @Success      200 {object} contacts.ListedContact "Default bot contact"
// @Failure      400 {string} string "Invalid request"
// @Failure      404 {string} string "Default bot not found"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/contacts/default-bot [get]
func (h *ContactsHander) GetDefaultBot(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	ch, err := util.GetWebsocket(r)
	if err != nil {
		http.Error(w, "Unable to get websocket", http.StatusBadRequest)
		return
	}

	botIdentifier := strings.TrimSpace(r.URL.Query().Get("bot"))
	if botIdentifier == "" {
		botIdentifier = strings.TrimSpace(r.URL.Query().Get("username"))
	}

	var botUser database.User
	var botQuery *gorm.DB
	if botIdentifier != "" {
		// `--bot` may be given as the bot's username, its display name or its
		// contact token; all three identify an automated user unambiguously.
		botQuery = DB.Where("is_automated = ?", true).
			Where("username = ? OR name = ? OR contact_token = ?", botIdentifier, botIdentifier, botIdentifier).
			Order("id asc")
	} else {
		botQuery = DB.Where("name = ?", "bot").Order("is_automated desc, id asc")
	}
	if err := botQuery.First(&botUser).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if botIdentifier != "" {
				http.Error(w, "Bot not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Default bot not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	// An explicit identifier must not become a directory of every automated
	// user on the platform: only the platform default, an existing contact, or
	// a public/owned bot runtime is addressable this way.
	if botIdentifier != "" && !canResolveBotForUser(DB, user, &botUser) {
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	// SetupBaseConnections only links users that exist at startup, so users
	// registered later have no contact row for the default bot. Ensure it here
	// so the default bot is always reachable.
	var contact database.Contact
	err = DB.Where("owning_user_id = ? AND contact_user_id = ?", user.ID, botUser.ID).First(&contact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		contact = database.Contact{
			OwningUserId:  user.ID,
			ContactUserId: botUser.ID,
		}
		if createErr := DB.Create(&contact).Error; createErr != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var publicProfile database.PublicProfile
	DB.Model(&database.PublicProfile{}).Where("user_id = ?", botUser.ID).First(&publicProfile)
	var profileData map[string]interface{}
	_ = json.Unmarshal(publicProfile.ProfileData, &profileData)
	if profileData == nil {
		profileData = map[string]interface{}{}
	}

	subscribers := ch.GetSubscribers()
	listedContact := ListedContact{
		ContactToken: botUser.ContactToken,
		Name:         botUser.Name,
		UserUUID:     botUser.UUID,
		IsOnline:     isSubscriber(subscribers, botUser.UUID),
		IsAutomated:  botUser.IsAutomated,
		ProfileData:  profileData,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(listedContact)
}

// canResolveBotForUser mirrors the bot API's readability rules for the
// `?bot=` lookup: the platform default bot, an existing contact, a public
// runtime, or a runtime the caller owns (directly or through the owners table).
func canResolveBotForUser(DB *gorm.DB, user *database.User, bot *database.User) bool {
	if user == nil || bot == nil {
		return false
	}
	if user.IsAdmin || bot.Name == "bot" {
		return true
	}
	var contactCount int64
	if err := DB.Model(&database.Contact{}).
		Where("owning_user_id = ? AND contact_user_id = ?", user.ID, bot.ID).
		Count(&contactCount).Error; err == nil && contactCount > 0 {
		return true
	}
	var runtime database.BotRuntimeConfig
	if err := DB.Where("bot_user_id = ? AND is_active = ?", bot.ID, true).First(&runtime).Error; err != nil {
		return false
	}
	if runtime.IsPublic || runtime.OwnerUserId == user.ID {
		return true
	}
	var ownerCount int64
	if err := DB.Model(&database.BotRuntimeOwner{}).
		Where("bot_runtime_config_id = ? AND user_id = ?", runtime.ID, user.ID).
		Count(&ownerCount).Error; err != nil {
		return false
	}
	return ownerCount > 0
}
