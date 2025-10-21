package contacts

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"net/http"

	"gorm.io/gorm"
)

// GetDefaultBot
// @Summary      Get the default platform bot contact
// @Description  Resolve the default platform bot (user named "bot") and return the current user's contact for it, creating the contact if it does not exist yet.
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Security     SessionAuth
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

	var botUser database.User
	if err := DB.Where("name = ?", "bot").Order("is_automated desc, id asc").First(&botUser).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Default bot not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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
