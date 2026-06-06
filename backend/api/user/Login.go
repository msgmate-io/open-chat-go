package user

import (
	"backend/api"
	"backend/database"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	integrationinterface "github.com/msgmate-io/go-integration-interface/integrationinterface"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"backend/logging"
)

type UserLogin struct {
	Email         string `json:"email"`
	Password      string `json:"password"`
	TwoFactorCode string `json:"two_factor_code"`
	RecoveryCode  string `json:"recovery_code"`
}

type LoginResponse struct {
	Success bool          `json:"success"`
	User    database.User `json:"user"`
}

func cookieDomainOverrideFromQuery(r *http.Request) (string, error) {
	q := r.URL.Query()
	raw := strings.TrimSpace(q.Get("cookie_domain"))
	if raw == "" {
		raw = strings.TrimSpace(q.Get("domain"))
	}
	if raw == "" {
		raw = strings.TrimSpace(q.Get("cookie_origin"))
	}
	if raw == "" {
		raw = strings.TrimSpace(q.Get("origin"))
	}
	if raw == "" {
		return "", nil
	}

	// If it's an origin URL (https://example.com:123), parse it.
	host := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("invalid cookie origin: %v", err)
		}
		host = u.Host
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", nil
	}

	// Strip port if present.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else if strings.Count(host, ":") == 0 {
		// no port, ok
	} else {
		// could be IPv6 without brackets or other invalid input; fall back to ParseIP check below
		host = strings.Trim(host, "[]")
	}
	host = strings.TrimSpace(strings.Trim(host, "."))

	if host == "" {
		return "", nil
	}

	// Browsers don't accept Domain cookies for IPs/localhost; use host-only cookie.
	if host == "localhost" || net.ParseIP(host) != nil {
		return "", nil
	}

	return host, nil
}

func clientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if idx := strings.Index(forwarded, ","); idx >= 0 {
			forwarded = forwarded[:idx]
		}
		if forwarded = strings.TrimSpace(forwarded); forwarded != "" {
			return forwarded
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// authMethodFromLoginPayload classifies a login attempt so login history can
// distinguish password, two-factor and recovery-code attempts.
func authMethodFromLoginPayload(data UserLogin) string {
	switch {
	case strings.TrimSpace(data.RecoveryCode) != "":
		return "recovery_code"
	case strings.TrimSpace(data.TwoFactorCode) != "":
		return "two_factor"
	default:
		return "password"
	}
}

// publishAuthEvent fans a single authentication attempt out to every
// integration that registered an auth event sink. Sink errors are logged and
// never affect the authentication result.
func publishAuthEvent(r *http.Request, DB *gorm.DB, userID uint, identifier, method string, success bool, failureReason string) {
	sinks := integrationinterface.AuthEventSinks()
	if len(sinks) == 0 {
		return
	}
	event := integrationinterface.AuthEvent{
		DB:            DB,
		UserID:        userID,
		Identifier:    identifier,
		Method:        method,
		Success:       success,
		FailureReason: failureReason,
		IP:            clientIPFromRequest(r),
		OccurredAt:    time.Now().UTC(),
	}
	if r != nil {
		event.UserAgent = strings.TrimSpace(r.Header.Get("User-Agent"))
	}
	for _, sink := range sinks {
		if err := sink.Sink(r.Context(), event); err != nil {
			logging.Errorf("integration %q auth event sink error: %v", sink.IntegrationName, err)
		}
	}
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
// @Param        cookie_origin query string false "Optional: cookie origin URL (e.g. https://app.example.com) used to derive the session cookie Domain"
// @Param        cookie_domain query string false "Optional: cookie domain (e.g. app.example.com) used for the session cookie Domain"
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
	method := authMethodFromLoginPayload(data)
	err, token, twofaRequired, user := LoginUser(DB, data.Email, data.Password, data.TwoFactorCode, data.RecoveryCode, expiry)
	if err != nil {
		publishAuthEvent(r, DB, user.ID, strings.TrimSpace(data.Email), method, false, err.Error())
		if twofaRequired {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"requires_two_factor": true, "error": err.Error()})
			return
		}
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	publishAuthEvent(r, DB, user.ID, strings.TrimSpace(data.Email), method, true, "")

	cookieDomain := h.CookieDomain
	if override, err := cookieDomainOverrideFromQuery(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if override != "" || (r.URL.Query().Has("cookie_domain") || r.URL.Query().Has("domain") || r.URL.Query().Has("cookie_origin") || r.URL.Query().Has("origin")) {
		// If the user explicitly passed a cookie override param and it resolved to empty, we want host-only cookie.
		cookieDomain = override
	}

	cookie := api.CreateSessionToken(w, r, cookieDomain, token, expiry)

	// Check if x-cookie-header query parameter is set to true
	if r.URL.Query().Get("x-cookie-header") == "true" {
		w.Header().Add("X-Set-Cookie", cookie.String())
	} else {
		w.Header().Add("Set-Cookie", cookie.String())
	}

	w.Header().Add("Cache-Control", `no-cache="Set-Cookie"`)

	http.SetCookie(w, &http.Cookie{
		Name:     "oc_client_state",
		Value:    "true",
		Path:     "/",
		MaxAge:   0,
		HttpOnly: false,
		Secure:   api.RequestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(LoginResponse{
		Success: true,
		User:    user,
	})
}

func LoginUser(DB *gorm.DB, email string, password string, twoFactorCode string, recoveryCode string, expiry time.Time) (error, string, bool, database.User) {
	var user database.User // TODO: sql injection?
	identifier := strings.TrimSpace(email)
	q := DB.First(&user, "email = ? OR username = ?", identifier, identifier)

	if q.Error != nil {
		logging.Errorf("login lookup failed for %q: %v", identifier, q.Error)
		return q.Error, "", false, database.User{}
	}

	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return err, "", false, database.User{}
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
				return fmt.Errorf("invalid recovery code"), "", true, database.User{}
			}
		} else {
			if twoFactorCode == "" {
				return fmt.Errorf("two-factor code required"), "", true, database.User{}
			}
			// Validate TOTP code
			if !VerifyTOTP(user.TwoFactorSecret, twoFactorCode, time.Now()) {
				return fmt.Errorf("invalid two-factor code"), "", true, database.User{}
			}
		}
	}
	if suspended, err := database.IsUserSuspended(DB, user.ID); err != nil {
		return err, "", false, database.User{}
	} else if suspended {
		// Global suspension: never hand out a session for a suspended account.
		return fmt.Errorf("account suspended"), "", false, user
	}
	if err := database.EnsureAccountStateRowForUser(DB, &user); err != nil {
		return err, "", false, database.User{}
	}

	token := api.GenerateToken()
	// The browser session is a 24h token (expiry above). The auth middleware
	// rejects it once expired (resolveValidSessionFromRequest) and the
	// database session sweeper removes the row an hour later
	// (database.StartSessionSweeper, ci#729 follow-up), so nothing accumulates
	// here.
	session := database.Session{
		Token:  token,
		Data:   []byte{},
		Expiry: expiry,
		UserId: user.ID,
	}

	q = DB.Create(&session)

	if q.Error != nil {
		return q.Error, "", false, database.User{}
	}
	return nil, token, false, user
}
