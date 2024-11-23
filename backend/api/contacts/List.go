package contacts

import (
	"backend/database"
	"encoding/json"
	"net/http"
	"strconv"
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
// @Param        page  query  int  false  "Page number"  default(1)
// @Param        limit query  int  false  "Page size"     default(10)
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
	q := database.DB.Scopes(database.Paginate(&contacts, &pagination, database.DB)).
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

	pagination.Rows = contacts

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
