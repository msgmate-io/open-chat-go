package user

import (
	"backend/database"
	"gorm.io/gorm"
	"net/http"
	"strings"
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

	tokens := make([]string, 0)
	seen := map[string]struct{}{}
	for _, cookie := range r.Cookies() {
		if cookie.Name != "session_id" {
			continue
		}
		token := strings.TrimSpace(cookie.Value)
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	if len(tokens) == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	result := DB.Where("token IN ?", tokens).Delete(&database.Session{})
	if result.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Clear the cookie by setting an expired cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    "",
		Path:     "/",
		Domain:   h.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	if h.CookieDomain != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Logout successful"))
}
