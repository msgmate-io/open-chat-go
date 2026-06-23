package server

// Some stuff stolen from 'https://github.com/dreamsofcode-io/nethttp'
import (
	"backend/database"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"gorm.io/gorm"
	"log"
	"net"
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

func (w *wrappedWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *wrappedWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *wrappedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}

	return hijacker.Hijack()
}

func (w *wrappedWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}

	return pusher.Push(target, opts)
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

func sessionTokensFromRequest(r *http.Request) []string {
	if r == nil {
		return nil
	}

	tokens := make([]string, 0)
	seen := map[string]struct{}{}
	for _, cookie := range r.Cookies() {
		if cookie.Name != "session_id" {
			continue
		}
		token := strings.TrimSpace(cookie.Value)
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	return tokens
}

func resolveValidSessionFromRequest(DB *gorm.DB, r *http.Request) (*database.Session, bool, error) {
	if DB == nil {
		return nil, false, nil
	}

	now := time.Now()
	for _, token := range sessionTokensFromRequest(r) {
		var session database.Session
		if err := DB.First(&session, "token = ?", token).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				continue
			}
			return nil, false, err
		}
		if session.Expiry.Before(now) {
			continue
		}
		return &session, true, nil
	}

	return nil, false, nil
}

func cookieSecureFromRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on") {
		return true
	}
	return false
}

func cookieDomainFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}

	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}

	host = strings.Trim(host, "[]")
	if host == "" {
		return ""
	}
	if host == "localhost" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return ""
	}

	return host
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	secure := cookieSecureFromRequest(r)

	hostOnlyCookie := &http.Cookie{
		Name:     "session_id",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, hostOnlyCookie)

	if domain := cookieDomainFromRequest(r); domain != "" {
		domainCookie := &http.Cookie{
			Name:     "session_id",
			Value:    "",
			Path:     "/",
			Domain:   domain,
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
		}
		http.SetCookie(w, domainCookie)
	}
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

		tokens := sessionTokensFromRequest(r)
		if len(tokens) == 0 {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		session, found, err := resolveValidSessionFromRequest(DB, r)
		if err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			log.Println(err)
			return
		}
		if !found {
			clearSessionCookie(w, r)
			http.Error(w, "Invalid token", http.StatusForbidden)
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

		tokens := sessionTokensFromRequest(r)
		if len(tokens) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		session, found, err := resolveValidSessionFromRequest(DB, r)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if !found {
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

		DB, _ := r.Context().Value("db").(*gorm.DB)
		_, authorized, _ := resolveValidSessionFromRequest(DB, r)
		if !authorized {
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
			http.Redirect(w, r, "/chat", http.StatusFound)
			return
		}

		next.ServeHTTP(w, r)
	})
}
