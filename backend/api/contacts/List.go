package contacts

import (
	"backend/api/websocket"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"strconv"
)

type ListedContact struct {
	ContactToken string                 `json:"contact_token"`
	Name         string                 `json:"name"`
	UserUUID     string                 `json:"user_uuid"`
	IsOnline     bool                   `json:"is_online"`
	IsAutomated  bool                   `json:"is_automated"`
	ProfileData  map[string]interface{} `json:"profile_data"`
}

func isSubscriber(subscribers []websocket.Subscriber, userUUID string) bool {
	for _, subscriber := range subscribers {
		if userUUID == subscriber.UserUUID {
			return true
		}
	}
	return false
}

func contactToContactListed(DB *gorm.DB, ch *websocket.WebSocketHandler, contacts []database.Contact, userId uint) []ListedContact {
	// check if the contact is online

	subscribers := ch.GetSubscribers()
	listedContacts := make([]ListedContact, len(contacts))

	// check if any contact.user.id is in the subscribers
	for i, contact := range contacts {
		var partner database.User
		if contact.ContactUserId == userId {
			partner = contact.OwningUser
		} else {
			partner = contact.ContactUser
		}

		// try to retrieve possible partner profile data
		// TODO: properly handle errors
		profileData := make(map[string]interface{})
		DB.Model(&database.PublicProfile{}).Where("user_id = ?", partner.ID).First(&profileData)
		// fmt.Println("profileData", profileData)

		if isSubscriber(subscribers, partner.UUID) {
			listedContacts[i] = ListedContact{
				ContactToken: partner.ContactToken,
				Name:         partner.Name,
				UserUUID:     partner.UUID,
				IsOnline:     true,
				IsAutomated:  partner.IsAutomated,
				ProfileData:  profileData,
			}
		} else {
			listedContacts[i] = ListedContact{
				ContactToken: partner.ContactToken,
				Name:         partner.Name,
				UserUUID:     partner.UUID,
				IsOnline:     false,
				IsAutomated:  partner.IsAutomated,
				ProfileData:  profileData,
			}
		}
	}

	return listedContacts
}

type PaginatedContacts struct {
	Limit      int             `json:"limit"`
	Page       int             `json:"page"`
	TotalPages int             `json:"total_pages"`
	Rows       []ListedContact `json:"rows"`
}

// List Contacts
// @Summary      Get user contacts
// @Description  Retrieve a list of contacts associated with a specific user ID
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        page  query  int  false  "Page number"  default(1)
// @Param        limit query  int  false  "Page size"     default(10)
// @Success      200 {object} contacts.PaginatedContacts "Paginated contacts"
// @Failure      400 {string} string "Invalid user ID"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/contacts/list [get]
func (h *ContactsHander) List(w http.ResponseWriter, r *http.Request) {
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

	pagination := database.Pagination{Page: 1, Limit: 10}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	// The caller-scoped filter must exist BEFORE pagination runs: Paginate counts
	// the rows to derive TotalPages, and the count used to run against the
	// unscoped handle (the Where clauses were chained after Scopes), so
	// TotalPages reflected every contact in the database instead of the caller's
	// (ci#747). Build the filtered query first - the same shape
	// backend/api/chats/List.go uses - and paginate that.
	query := DB.
		Where("owning_user_id = ?", user.ID).
		Where("deleted_at IS NULL")

	var contacts []database.Contact
	q := query.Scopes(database.Paginate(&contacts, &pagination, query)).
		Preload("ContactUser").
		Find(&contacts)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// A page past the end is NOT an error (ci#747). The python client's
	// get_bot(identifier) GETs /api/v1/bots/<identifier> first and, when that
	// answers 404/409 (unknown, duplicated or not-owned bot), falls back to
	// GET /api/v1/contacts/list. Answering 404 for the out-of-range page turned
	// that fallback into `RuntimeError: get_bot fallback failed with status
	// 404`, so a bot that simply has no contact row looked like a broken
	// backend. Return an empty page (200) instead: `contacts` stays empty, so
	// contactToContactListed yields a non-nil empty slice and `rows` serialises
	// as [] rather than null.

	response := PaginatedContacts{
		Limit:      pagination.Limit,
		Page:       pagination.Page,
		TotalPages: pagination.TotalPages,
		Rows:       contactToContactListed(DB, ch, contacts, user.ID),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetContactByToken
// @Summary      Get contact by token
// @Description  Retrieve a single contact by their contact token
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        contact_token path string true "Contact token"
// @Success      200 {object} contacts.ListedContact
// @Failure      400 {string} string "Invalid request"
// @Failure      404 {string} string "Contact not found"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/contacts/{contact_token} [get]
func (h *ContactsHander) GetContactByToken(w http.ResponseWriter, r *http.Request) {
	DB, _, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	ch, err := util.GetWebsocket(r)
	if err != nil {
		http.Error(w, "Unable to get websocket", http.StatusBadRequest)
		return
	}

	token := r.PathValue("contact_token")
	if token == "" {
		http.Error(w, "Contact token is required", http.StatusBadRequest)
		return
	}

	var user database.User
	if err := DB.Where("contact_token = ?", token).First(&user).Error; err != nil {
		http.Error(w, "Contact not found", http.StatusNotFound)
		return
	}

	var publicProfile database.PublicProfile
	DB.Model(&database.PublicProfile{}).Where("user_id = ?", user.ID).First(&publicProfile)
	// Now parse the profile data
	var profileData map[string]interface{}
	json.Unmarshal(publicProfile.ProfileData, &profileData)
	if profileData == nil {
		profileData = map[string]interface{}{}
	}
	// fmt.Println("profileData", profileData)

	subscribers := ch.GetSubscribers()
	listedContact := ListedContact{
		ContactToken: user.ContactToken,
		Name:         user.Name,
		UserUUID:     user.UUID,
		IsOnline:     isSubscriber(subscribers, user.UUID),
		IsAutomated:  user.IsAutomated,
		ProfileData:  profileData,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listedContact)
}

// GetContactByChatUUID
// @Summary      Get contact by chat UUID
// @Description  Retrieve a single contact based on a chat UUID
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Param        chat_uuid path string true "Chat UUID"
// @Success      200 {object} contacts.ListedContact
// @Failure      400 {string} string "Invalid request"
// @Failure      404 {string} string "Chat or contact not found"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/chats/{chat_uuid}/contact [get]
func (h *ContactsHander) GetContactByChatUUID(w http.ResponseWriter, r *http.Request) {
	DB, currentUser, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	ch, err := util.GetWebsocket(r)
	if err != nil {
		http.Error(w, "Unable to get websocket", http.StatusBadRequest)
		return
	}

	chatUUID, err := util.ParsePathUUID(r, "chat_uuid")
	if err != nil {
		http.Error(w, "Chat UUID is required", http.StatusBadRequest)
		return
	}

	// First find the chat and ensure the current user has access to it
	var chat database.Chat
	if err := DB.Where("uuid = ? AND (user1_id = ? OR user2_id = ?)",
		chatUUID, currentUser.ID, currentUser.ID).
		Preload("User1").
		Preload("User2").
		First(&chat).Error; err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}

	// Determine which user is the contact (the other party in the chat)
	var contactUser database.User
	if chat.User1Id == currentUser.ID {
		contactUser = chat.User2
	} else {
		contactUser = chat.User1
	}

	// Get the contact's public profile
	var publicProfile database.PublicProfile
	DB.Model(&database.PublicProfile{}).Where("user_id = ?", contactUser.ID).First(&publicProfile)

	// Parse the profile data
	var profileData map[string]interface{}
	json.Unmarshal(publicProfile.ProfileData, &profileData)
	if profileData == nil {
		profileData = map[string]interface{}{}
	}

	subscribers := ch.GetSubscribers()
	listedContact := ListedContact{
		ContactToken: contactUser.ContactToken,
		Name:         contactUser.Name,
		UserUUID:     contactUser.UUID,
		IsOnline:     isSubscriber(subscribers, contactUser.UUID),
		IsAutomated:  contactUser.IsAutomated,
		ProfileData:  profileData,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listedContact)
}
