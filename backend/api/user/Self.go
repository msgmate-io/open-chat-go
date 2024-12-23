package user

import (
	"backend/database"
	"encoding/json"
	"net/http"
)

// Self returns the current user's details.
//
//	@Summary      Get current user
//	@Description  Retrieve the current user's information
//	@Tags         users
//	@Accept       json
//	@Produce      json
//	@Success      200 {object} database.User "Current user details"
//	@Failure      400 {string} string "Invalid user ID"
//	@Router       /api/v1/users/self [get]
func (h *UserHandler) Self(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user) // password_hash is not included (database.User)
}
