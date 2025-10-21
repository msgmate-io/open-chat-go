package user

import (
	"backend/database"
	"encoding/json"
	"net/http"
)

// SelfResponse is the current user plus, when the session is an impersonation,
// the admin behind it so the UI can clearly mark the session.
type SelfResponse struct {
	database.User
	Impersonator *database.User `json:"impersonator,omitempty"`
}

// Self returns the current user's details.
//
//	@Summary      Get current user
//	@Description  Retrieve the current user's information
//	@Tags         users
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Success      200 {object} database.User "Current user details"
//	@Failure      400 {string} string "Invalid user ID"
//	@Router       /api/v1/user/self [get]
func (h *UserHandler) Self(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	response := SelfResponse{User: *user}
	if impersonator, ok := r.Context().Value("impersonator").(*database.User); ok && impersonator != nil {
		response.Impersonator = impersonator
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response) // password_hash is not included (database.User)
}
