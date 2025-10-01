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
	Email         string `json:"email"`
	Password      string `json:"password"`
	TwoFactorCode string `json:"two_factor_code"`
	RecoveryCode  string `json:"recovery_code"`
}

// curl -X POST -H "Content-Type: application/json" -H "Origin: localhost:8080" -d '{"email":"tim+test@timschupp.de","password":"password"}' http://localhost:8080/api/v1/user/login -v
// https://stackoverflow.com/questions/23259586/bcrypt-password-hashing-in-golang-compatible-with-node-js

// Login a user
//
// @Summary      Login a user
// @Description  Authenticate and login a user with email and password
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        request body UserLogin true "Login credentials"
// @Success      200  {string}  string "Login successful"
// @Failure      400  {string}  string "Invalid email or password"
// @Failure      500  {string}  string "Internal server error"
// @Router       /api/v1/user/login [post]
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
	err, token, twofaRequired := LoginUser(DB, data.Email, data.Password, data.TwoFactorCode, data.RecoveryCode, expiry)
	if err != nil {
		if twofaRequired {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"requires_two_factor": true, "error": err.Error()})
			return
		}
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	cookie := api.CreateSessionToken(w, h.CookieDomain, token, expiry)

	// Check if x-cookie-header query parameter is set to true
	if r.URL.Query().Get("x-cookie-header") == "true" {
		w.Header().Add("X-Set-Cookie", cookie.String())
	} else {
		w.Header().Add("Set-Cookie", cookie.String())
	}

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
	err, token, twofaRequired := LoginUser(DB, data.Email, data.Password, data.TwoFactorCode, data.RecoveryCode, expiry)
	if err != nil {
		if twofaRequired {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"requires_two_factor": true, "error": err.Error()})
			return
		}
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	cookie := api.CreateSessionToken(w, h.CookieDomain, token, expiry)

	// Check if x-cookie-header query parameter is set to true
	if r.URL.Query().Get("x-cookie-header") == "true" {
		w.Header().Add("X-Set-Cookie", cookie.String())
	} else {
		w.Header().Add("Set-Cookie", cookie.String())
	}

	w.Header().Add("Cache-Control", `no-cache="Set-Cookie"`)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful"))
}

func LoginUser(DB *gorm.DB, email string, password string, twoFactorCode string, recoveryCode string, expiry time.Time) (error, string, bool) {
	var user database.User // TODO: sql injection?
	q := DB.First(&user, "email = ?", email)

	if q.Error != nil {
		fmt.Println(q.Error)
		return q.Error, "", false
	}

	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return err, "", false
	}

	// Enforce two-factor if enabled for this user
	if user.TwoFactorEnabled {
		// Try recovery code first if provided
		if recoveryCode != "" {
			var recCodes []database.TwoFactorRecoveryCode
			DB.Where("user_id = ? AND used_at IS NULL", user.ID).Find(&recCodes)
			matched := false
			for _, rc := range recCodes {
				if bcrypt.CompareHashAndPassword([]byte(rc.CodeHash), []byte(recoveryCode)) == nil {
					now := time.Now()
					DB.Model(&rc).Update("used_at", &now)
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("invalid recovery code"), "", true
			}
		} else {
			if twoFactorCode == "" {
				return fmt.Errorf("two-factor code required"), "", true
			}
			// Validate TOTP code
			if !VerifyTOTP(user.TwoFactorSecret, twoFactorCode, time.Now()) {
				return fmt.Errorf("invalid two-factor code"), "", true
			}
		}
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
		return q.Error, "", false
	}
	return nil, token, false
}
