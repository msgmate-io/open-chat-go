package user

import (
	"backend/database"
	"encoding/json"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/mail"
)

type UserRegister struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
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
		ContactToken: uuid.New().String(),
		IsAdmin:      false,
	}

	q = database.DB.Create(&user)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("User created"))
}
