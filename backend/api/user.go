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

type UserRegister struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// curl -X POST -H "Content-Type: application/json" -H "Origin: localhost:8080" -d '{"email":"tim+test@timschupp.de","password":"password"}' http://localhost:8080/api/v1/user/login -v
// https://stackoverflow.com/questions/23259586/bcrypt-password-hashing-in-golang-compatible-with-node-js

// Login a user
//
//		@Summary      Login a user
//		@Description  Login a user
//		@Tags         accounts
//		@Accept       json
//		@Produce      json
//	 	@Param        email body string true "Email"
//	 	@Param        password body string true "Password"
//		@Success      200  {string}  string	"Login successful"
//		@Success      200  {string}  string	"Login successful"
//		@Failure      400  {string}  string	"Invalid email or password"
//		@Failure      500  {object}  string	"Internal server error"
//		@Router       /api/v1/user/login [post]
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

	cookie := CreateSessionToken(w, token, expiry)

	w.Header().Add("Set-Cookie", cookie.String())
	w.Header().Add("Cache-Control", `no-cache="Set-Cookie"`)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful"))
}

// curl -X POST -H "Content-Type: application/json" -H "Origin: localhost:1984" -d '{"name": "Tim Here", "email":"tim+test@timschupp.de","password":"password"}' http://localhost:1984/api/v1/user/register -v

// Register a user
//
//	@Summary      Register a user
//	@Description  Register a user
//	@Tags         accounts
//	@Accept       json
//	@Produce      json
//	@Param        name body string true "Name"
//	@Param        email body string true "Email"
//	@Param        password body string true "Password"
//	@Success      201  {string}  string	"User created"
//	@Failure      400  {string}  string	"Invalid email"
//	@Failure      400  {string}  string	"Email already in use"
//	@Failure      400  {string}  string	"Password too short"
//	@Failure      500  {string}  string	"Internal server error"
//	@Router       /api/v1/user/register [post]
func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	var data UserRegister

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	_, err := mail.ParseAddress(data.Email)
	if err != nil {
		http.Error(w, "Invalid email", http.StatusBadRequest)
		return
	}

	var user database.User
	q := database.DB.First(&user, "email = ?", data.Email)

	if q.Error == nil {
		http.Error(w, "Email already in use", http.StatusBadRequest)
		return
	}

	// TODO: check password strength
	if len(data.Password) < 8 {
		http.Error(w, "Password too short", http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(data.Password), bcrypt.DefaultCost)

	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	user = database.User{
		Name:         data.Name,
		Email:        data.Email,
		PasswordHash: string(hashedPassword),
	}

	q = database.DB.Create(&user)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("User created"))
}
