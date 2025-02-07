package user

import (
	"backend/database"
	"gorm.io/gorm"
	"net/http"
)

// Logout a user
//
//	@Summary      Logout a user
//	@Description  Invalidates the user's session token
//	@Tags         accounts
//	@Accept       json
//	@Produce      json
//	@Success      200  {string}  string	"Logout successful"
//	@Failure      401  {string}  string	"Unauthorized"
//	@Failure      500  {string}  string	"Internal server error"
//	@Router       /api/v1/user/logout [post]
func (h *UserHandler) Logout(w http.ResponseWriter, r *http.Request) {
	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusInternalServerError)
		return
	}

	cookie, err := r.Cookie("session_id")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	result := DB.Where("token = ?", cookie.Value).Delete(&database.Session{})
	if result.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Clear the cookie by setting an expired cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Logout successful"))
}
