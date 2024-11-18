package contacts

import (
	"backend/database"
	"encoding/json"
	"net/http"
)

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
// @Success      200 {array}  contacts.PaginatedContacts "List of contacts"
// @Failure      400 {string} string "Invalid user ID"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/contacts/list [get]
func (h *ContactsHander) List(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var contacts []database.Contact
	var pagination database.Pagination = database.Pagination{Page: 1, Limit: 10}
	q := database.DB.Scopes(database.Paginate(&contacts, &pagination, database.DB)).
		Where("owning_user_id = ?", user.ID).
		Where("deleted_at IS NULL").
		Find(&contacts)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	pagination.Rows = contacts

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
