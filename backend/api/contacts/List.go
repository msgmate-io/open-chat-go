package contacts

import (
	"backend/api/websocket"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
)

type ListedContact struct {
	ContactToken string `json:"contact_token"`
	Name         string `json:"name"`
	UserUUID     string `json:"user_uuid"`
	IsOnline     bool   `json:"is_online"`
}

func isSubscriber(subscribers []websocket.Subscriber, userUUID string) bool {
	for _, subscriber := range subscribers {
		if userUUID == subscriber.UserUUID {
			return true
		}
	}
	return false
}

func contactToContactListed(ch *websocket.WebSocketHandler, contacts []database.Contact, userId uint) []ListedContact {
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
		if isSubscriber(subscribers, partner.UUID) {
			listedContacts[i] = ListedContact{
				ContactToken: partner.ContactToken,
				Name:         partner.Name,
				UserUUID:     partner.UUID,
				IsOnline:     true,
			}
		} else {
			listedContacts[i] = ListedContact{
				ContactToken: partner.ContactToken,
				Name:         partner.Name,
				UserUUID:     partner.UUID,
				IsOnline:     false,
			}
		}
	}

	return listedContacts
}

type PaginatedContacts struct {
	database.Pagination
	Rows []database.Contact
}

// List Contacts
// @Summary      Get user contacts
// @Description  Retrieve a list of contacts associated with a specific user ID
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Param        page  query  int  false  "Page number"  default(1)
// @Param        limit query  int  false  "Page size"     default(10)
// @Success      200 {array}  contacts.PaginatedContacts "List of contacts"
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

	var contacts []database.Contact
	q := DB.Scopes(database.Paginate(&contacts, &pagination, DB)).
		Where("owning_user_id = ?", user.ID).
		Where("deleted_at IS NULL").
		Preload("ContactUser").
		Find(&contacts)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(contacts) == 0 && pagination.Page > 1 {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}

	pagination.Rows = contactToContactListed(ch, contacts, user.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
