package api

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
	"time"
)

// RequestIsSecure reports whether the request reached the server over HTTPS,
// either directly (TLS) or via a trusted reverse proxy that signals it through
// X-Forwarded-Proto / X-Forwarded-Ssl.
func RequestIsSecure(r *http.Request) bool {
	return r != nil && (r.TLS != nil ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on"))
}

func GenerateToken(tokenBase string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(tokenBase), bcrypt.DefaultCost)

	if err != nil {
		panic(fmt.Errorf("failed to generate token: %w", err))
	}

	sum := sha256.Sum256(hash)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func CreateSessionToken(w http.ResponseWriter, r *http.Request, domain string, token string, expiry time.Time) *http.Cookie {
	persist := true

	secure := RequestIsSecure(r)

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
