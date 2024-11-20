package contacts

import (
	"backend/database"
	"encoding/json"
	"net/http"
)

type AddContact struct {
	ContactToken string `json:"contact_token"`
}

// Add a contact
// @Summary      Add a contact
// @Description  Add a contact
// @Tags         contacts
// @Accept       json
// @Produce      json
// @Param        contact_token body string true "Contact token"
// @Success      200  {string}  string	"Contact added"
// @Failure      400  {string}  string	"Invalid contact token"
// @Failure      500  {object}  string	"Internal server error"
// @Router       /api/v1/contacts/add [post]
func (h *ContactsHander) Add(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var data AddContact
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var otherUser database.User
	if err := database.DB.First(&otherUser, "contact_token = ?", data.ContactToken).Error; err != nil {
		http.Error(w, "Invalid contact token", http.StatusBadRequest)
		return
	}

	if otherUser.ID == user.ID {
		http.Error(w, "Cannot add yourself as a contact", http.StatusBadRequest)
		return
	}

	var contact database.Contact
	if err := database.DB.First(&contact, "owning_user_id = ? AND contact_user_id = ?", user.ID, otherUser.ID).Error; err == nil {
		http.Error(w, "Contact already exists", http.StatusBadRequest)
		return
	}

	newContact := database.Contact{
		OwningUserId:  user.ID,
		ContactUserId: otherUser.ID,
		ContactToken:  otherUser.ContactToken,
	}

	database.DB.Create(&newContact)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Contact added"))
}
