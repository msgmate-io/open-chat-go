package api

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
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

func CreateSessionToken(w http.ResponseWriter, token string, expiry time.Time) *http.Cookie {
	persist := true
	cookie := &http.Cookie{
		Name:     "session_id",
		Value:    token,
		Path:     "/",
		Domain:   "localhost",
		Secure:   true,
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
