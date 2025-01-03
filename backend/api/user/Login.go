package user

import (
	"backend/api"
	"backend/database"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/http"
	"time"
)

type UserLogin struct {
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
//		@Failure      400  {string}  string	"Invalid email or password"
//		@Failure      500  {object}  string	"Internal server error"
//		@Router       /api/v1/user/login [post]
func (h *UserHandler) Login(w http.ResponseWriter, r *http.Request) {
	var data UserLogin
	var defaultErrorMessage string = "Invalid email or password"

	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// deliberately email requirements are only enforce on registration
	// sucht that we may have admin / bot users with single user names
	//_, err := mail.ParseAddress(data.Email)
	//if err != nil {
	//	http.Error(w, "Not a valid email", http.StatusBadRequest)
	//	return
	//}

	if data.Password == "" {
		http.Error(w, defaultErrorMessage, http.StatusBadRequest)
		return
	}

	var user database.User // TODO: sql injection?
	q := DB.First(&user, "email = ?", data.Email)

	if q.Error != nil {
		http.Error(w, defaultErrorMessage, http.StatusNotFound)
		return
	}

	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(data.Password))
	if err != nil {
		http.Error(w, defaultErrorMessage, http.StatusUnauthorized)
		return
	}

	expiry := time.Now().Add(24 * time.Hour)
	token := api.GenerateToken(user.Email) //TODO: based on something else! or random!
	// TODO: make sure sessions expire!
	session := database.Session{
		Token:  token,
		Data:   []byte{},
		Expiry: expiry,
		UserId: user.ID,
	}

	q = DB.Create(&session)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	cookie := api.CreateSessionToken(w, token, expiry)

	w.Header().Add("Set-Cookie", cookie.String())
	w.Header().Add("Cache-Control", `no-cache="Set-Cookie"`)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful"))
}
