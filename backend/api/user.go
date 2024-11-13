package api

import (
	"backend/database"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/mail"
	"time"
)

type UserHandler struct{}

type UserLogin struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// curl -X POST -H "Content-Type: application/json" -d '{"email":"tim+test@timschupp.de","password":"password"}' http://localhost:1984/api/v1/user/login
// https://stackoverflow.com/questions/23259586/bcrypt-password-hashing-in-golang-compatible-with-node-js
func (h *UserHandler) Login(w http.ResponseWriter, r *http.Request) {
	var data UserLogin
	var defaultErrorMessage string = "Invalid email or password"

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	_, err := mail.ParseAddress(data.Email)
	if err != nil {
		http.Error(w, defaultErrorMessage, http.StatusBadRequest)
		return
	}

	if data.Password == "" {
		http.Error(w, defaultErrorMessage, http.StatusBadRequest)
		return
	}

	var user database.User
	q := database.DB.First(&user, "email = ?", data.Email)

	if q.Error != nil {
		http.Error(w, defaultErrorMessage, http.StatusNotFound)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(data.Password))
	if err != nil {
		http.Error(w, defaultErrorMessage, http.StatusUnauthorized)
		return
	}

	expiry := time.Now().Add(24 * time.Hour)
	token := GenerateToken(user.Email) //TODO: based on something else! or random!
	session := database.Session{
		Token:  token,
		Data:   []byte{},
		Expiry: expiry,
	}

	q = database.DB.Create(&session)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	cookie := CreateSessionToken(w, "session_id", time.Now().Add(24*time.Hour))

	w.Header().Add("Set-Cookie", cookie.String())
	w.Header().Add("Cache-Control", `no-cache="Set-Cookie"`)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful"))
}
