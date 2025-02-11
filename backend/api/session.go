package api

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
	"time"
)

func GenerateToken(tokenBase string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(tokenBase), bcrypt.DefaultCost)

	if err != nil {
		panic(fmt.Errorf("failed to generate token: %w", err))
	}

	hasher := md5.New()
	hasher.Write(hash)
	return hex.EncodeToString(hasher.Sum(nil))
}

func CreateSessionToken(w http.ResponseWriter, r *http.Request, domain string, token string, expiry time.Time) *http.Cookie {
	persist := true

	secure := false
	if r != nil {
		if r.TLS != nil {
			secure = true
		}
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			secure = true
		}
		if strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on") {
			secure = true
		}
	}

	cookie := &http.Cookie{
		Name:     "session_id",
		Value:    token,
		Path:     "/",
		Domain:   domain,
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}

	if expiry.IsZero() {
		cookie.Expires = time.Unix(1, 0)
		cookie.MaxAge = -1
	} else if persist {
		cookie.Expires = time.Unix(expiry.Unix()+1, 0)
		cookie.MaxAge = int(time.Until(expiry).Seconds() + 1)
	}

	return cookie
}
