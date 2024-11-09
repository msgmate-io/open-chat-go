package api

import (
	"backend/database"
	"encoding/json"
	"log"
	"net/http"
	"net/mail"
)

type UserHandler struct{}

type UserLogin struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// curl -X POST -H "Content-Type: application/json" -d '{"email":"tim+test@timschupp.de","password":"password"}' http://localhost:1984/api/v1/user/login
func (h *UserHandler) Login(w http.ResponseWriter, r *http.Request) {
	var data UserLogin

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	_, err := mail.ParseAddress(data.Email)
	if err != nil {
		http.Error(w, "Invalid email", http.StatusBadRequest)
		return
	}

	if data.Password == "" {
		http.Error(w, "Invalid password", http.StatusBadRequest)
		return
	}

	var user database.User
	q := database.DB.First(&user, "email = ?", "tim+test@timschupp.de")

	if q.Error != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	log.Println(user)

}
