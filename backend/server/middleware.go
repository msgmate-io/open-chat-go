package server

// Some stuff stolen from 'https://github.com/dreamsofcode-io/nethttp'
import (
	"backend/database"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"gorm.io/gorm"
	"log"
	"net/http"
	"strings"
	"time"
)

type Middleware func(http.Handler) http.Handler

func CreateStack(xs ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(xs) - 1; i >= 0; i-- {
			x := xs[i]
			next = x(next)
		}

		return next
	}
}

type wrappedWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *wrappedWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
	w.statusCode = statusCode
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrapped := &wrappedWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(wrapped, r)

		log.Println(wrapped.statusCode, r.Method, r.URL.Path, time.Since(start))

		if jsonData := r.Context().Value("json"); jsonData != nil {
			log.Printf("JSON Body: %v", jsonData)
		}
	})
}

const UserContextKey = "user"

func UserFromContext(ctx context.Context) *database.User {
	user, ok := ctx.Value(UserContextKey).(*database.User)
	if !ok {
		return nil
	}
	return user
}

func resolveUserFromBearerToken(DB *gorm.DB, r *http.Request) (*database.User, bool) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" || !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return nil, false
	}
	rawToken := strings.TrimSpace(authHeader[7:])
	if rawToken == "" {
		return nil, false
	}
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])

	var accessToken database.AccessToken
	if err := DB.Where("token_hash = ?", tokenHash).First(&accessToken).Error; err != nil {
		return nil, false
	}
	if accessToken.RevokedAt != nil {
		return nil, false
	}
	if accessToken.ExpiresAt != nil && accessToken.ExpiresAt.Before(time.Now()) {
		return nil, false
	}

	var user database.User
	if err := DB.First(&user, "id = ?", accessToken.UserId).Error; err != nil {
		return nil, false
	}

	now := time.Now()
	DB.Model(&database.AccessToken{}).Where("id = ?", accessToken.ID).Update("last_used_at", &now)

	return &user, true
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		DB, ok := r.Context().Value("db").(*gorm.DB)
		if !ok {
			http.Error(w, "Unable to get database", http.StatusBadRequest)
			return
		}

		if user, ok := resolveUserFromBearerToken(DB, r); ok {
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		cookie, err := r.Cookie("session_id")
		// log.Println(cookie)
		if err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		token := strings.TrimSpace(cookie.Value)

		var session database.Session
		if err := DB.First(&session, "token = ?", token).Error; err != nil {

			if err == gorm.ErrRecordNotFound {
				http.Error(w, "Invalid token", http.StatusForbidden)
				return
			}

			http.Error(w, "Forbidden", http.StatusForbidden)
			log.Println(err)
			return
		}

		if session.Expiry.Before(time.Now()) {
			http.Error(w, "Session expired", http.StatusForbidden)
			return
		}

		var user database.User
		if err := DB.First(&user, "id = ?", session.UserId).Error; err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func OptionalAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		DB, ok := r.Context().Value("db").(*gorm.DB)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		if user, ok := resolveUserFromBearerToken(DB, r); ok {
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		cookie, err := r.Cookie("session_id")
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		token := strings.TrimSpace(cookie.Value)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		var session database.Session
		if err := DB.First(&session, "token = ?", token).Error; err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if session.Expiry.Before(time.Now()) {
			next.ServeHTTP(w, r)
			return
		}

		var user database.User
		if err := DB.First(&user, "id = ?", session.UserId).Error; err != nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var PublicRoutes = []string{"/", "/docs", "/models", "/tools", "/interaction"}

func isPublicFrontendRoute(path string) bool {
	for _, route := range PublicRoutes {
		if path == route || strings.HasPrefix(path, route+"/") {
			return true
		}
	}
	return false
}

func FrontendAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "text/html") {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie("session_id")
		if err != nil {
			http.SetCookie(w, &http.Cookie{
				Name:     "is_authorized",
				Value:    "false",
				Path:     "/",
				MaxAge:   0,
				HttpOnly: false,
				Secure:   false,
				SameSite: http.SameSiteStrictMode,
			})
			if isPublicFrontendRoute(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/login" {
				next.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}

		if cookie.Expires.Before(time.Now()) {
			http.SetCookie(w, &http.Cookie{
				Name:     "is_authorized",
				Value:    "true",
				Path:     "/",
				MaxAge:   0,
				HttpOnly: false,
				Secure:   false,
				SameSite: http.SameSiteStrictMode,
			})

			if r.URL.Path == "/login" {
				// autorized user on the login page can be redirected to /chat
				http.Redirect(w, r, "/chat", http.StatusFound)
				return
			}

			next.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
