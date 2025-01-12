package user

import (
	"backend/api"
	"backend/database"
	"encoding/json"
	"fmt"
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

	expiry := time.Now().Add(24 * time.Hour)
	err, token := LoginUser(DB, data.Email, data.Password, expiry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	cookie := api.CreateSessionToken(w, token, expiry)
	w.Header().Add("Set-Cookie", cookie.String())
	w.Header().Add("Cache-Control", `no-cache="Set-Cookie"`)

	http.SetCookie(w, &http.Cookie{
		Name:     "is_authorized",
		Value:    "true",
		Path:     "/",
		MaxAge:   0,
		HttpOnly: false,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful"))
}

func (h *UserHandler) NetworkUserLogin(w http.ResponseWriter, r *http.Request) {
	var data UserLogin

	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var networks []database.Network
	DB.Where("network_name = ?", data.Email).Find(&networks)

	if len(networks) == 0 {
		http.Error(w, "User is not a member of any network", http.StatusBadRequest)
		return
	}

	expiry := time.Now().Add(24 * time.Hour)
	err, token := LoginUser(DB, data.Email, data.Password, expiry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	cookie := api.CreateSessionToken(w, token, expiry)
	w.Header().Add("Set-Cookie", cookie.String())
	w.Header().Add("Cache-Control", `no-cache="Set-Cookie"`)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful"))
}

func LoginUser(DB *gorm.DB, email string, password string, expiry time.Time) (error, string) {
	var user database.User // TODO: sql injection?
	q := DB.First(&user, "email = ?", email)

	if q.Error != nil {
		fmt.Println(q.Error)
		return q.Error, ""
	}

	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return err, ""
	}

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
		return q.Error, ""
	}
	return nil, token
}
