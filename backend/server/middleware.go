package server

// Some stuff stolen from 'https://github.com/dreamsofcode-io/nethttp'
import (
	"backend/database"
	"backend/integrations"
	"backend/runtimecfg"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"gorm.io/gorm"
	"log"
	"net"
	"net/http"
	"net/url"
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

func APINoCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI := ""
		if r != nil {
			requestURI = strings.TrimSpace(r.RequestURI)
		}
		isAPIPath := r != nil && (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/"))
		isAPIRequestURI := requestURI == "/api" || strings.HasPrefix(requestURI, "/api/")
		if isAPIPath || isAPIRequestURI {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		next.ServeHTTP(w, r)
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

func resolveUserFromBearerToken(DB *gorm.DB, r *http.Request) (*database.User, *database.AccessToken, bool) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" || !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return nil, nil, false
	}
	rawToken := strings.TrimSpace(authHeader[7:])
	if rawToken == "" {
		return nil, nil, false
	}
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])

	var accessToken database.AccessToken
	if err := DB.Where("token_hash = ?", tokenHash).First(&accessToken).Error; err != nil {
		return nil, nil, false
	}
	if accessToken.RevokedAt != nil {
		return nil, nil, false
	}
	if accessToken.ExpiresAt != nil && accessToken.ExpiresAt.Before(time.Now()) {
		return nil, nil, false
	}

	// Restricted (audience-bound) tokens are default-deny: they are only
	// valid on explicitly allowlisted routes and must carry the required
	// scope for the route.
	if strings.TrimSpace(accessToken.Audience) != "" {
		requiredScope, routeAllowed := matchBrowserTokenRoute(r.Method, r.URL.Path)
		if !routeAllowed {
			return nil, nil, false
		}
		if requiredScope != "" && !accessToken.HasScope(requiredScope) {
			return nil, nil, false
		}
	}

	// Tokens derived from a parent credential (e.g. exchanged browser
	// tokens) become invalid as soon as the parent is revoked or expired.
	if accessToken.ParentTokenId != nil {
		var parent database.AccessToken
		if err := DB.First(&parent, "id = ?", *accessToken.ParentTokenId).Error; err != nil {
			return nil, nil, false
		}
		if parent.RevokedAt != nil {
			return nil, nil, false
		}
		if parent.ExpiresAt != nil && parent.ExpiresAt.Before(time.Now()) {
			return nil, nil, false
		}
	}

	var user database.User
	if err := DB.First(&user, "id = ?", accessToken.UserId).Error; err != nil {
		return nil, nil, false
	}
	if err := database.EnsureAccountStateRowForUser(DB, &user); err != nil {
		return nil, nil, false
	}

	now := time.Now()
	DB.Model(&database.AccessToken{}).Where("id = ?", accessToken.ID).Update("last_used_at", &now)

	return &user, &accessToken, true
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

func isEmailVerificationExemptAPIPath(path string) bool {
	switch path {
	case "/api/v1/user/self", "/api/v1/user/logout", "/api/v1/integrations/account_management/email-verification/status", "/api/v1/integrations/account_management/email-verification/request", "/api/v1/integrations/account_management/email-verification/verify":
		return true
	default:
		return false
	}
}

func enforceEmailVerificationForAPI(DB *gorm.DB, r *http.Request, userID uint) error {
	if !database.RequireEmailVerificationFromRuntimeConfig() {
		return nil
	}
	verified, err := database.IsUserEmailVerified(DB, userID)
	if err != nil {
		return err
	}
	if verified {
		return nil
	}
	if isEmailVerificationExemptAPIPath(r.URL.Path) {
		return nil
	}
	return errors.New("email verification required")
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

		if user, accessToken, ok := resolveUserFromBearerToken(DB, r); ok {
			if err := enforceEmailVerificationForAPI(DB, r, user.ID); err != nil {
				http.Error(w, "email verification required", http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			ctx = database.ContextWithAccessToken(ctx, accessToken)
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
		if err := database.EnsureAccountStateRowForUser(DB, &user); err != nil {
			http.Error(w, "Unable to load user state", http.StatusInternalServerError)
			return
		}
		if err := enforceEmailVerificationForAPI(DB, r, user.ID); err != nil {
			http.Error(w, "email verification required", http.StatusForbidden)
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

		if user, accessToken, ok := resolveUserFromBearerToken(DB, r); ok {
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			ctx = database.ContextWithAccessToken(ctx, accessToken)
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
		if err := database.EnsureAccountStateRowForUser(DB, &user); err != nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var PublicRoutes = []string{"/", "/docs", "/models", "/tools", "/interaction", "/callback", "/signup-request-send", "/sign-up", "/email-verification", "/mobile"}

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
		if strings.EqualFold(strings.TrimSpace(runtimecfg.GetAll()["MOBILE_ROUTE_API_WS_TO_UPSTREAM"].Value), "true") {
			next.ServeHTTP(w, r)
			return
		}

		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "text/html") {
			next.ServeHTTP(w, r)
			return
		}

		DB, _ := r.Context().Value("db").(*gorm.DB)
		session, authorized, _ := resolveValidSessionFromRequest(DB, r)
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
			target := r.URL.RequestURI()
			if !strings.HasPrefix(target, "/") {
				target = "/chat"
			}
			escapedTarget := url.QueryEscape(target)
			http.Redirect(w, r, "/login?redirect="+escapedTarget+"&next="+escapedTarget, http.StatusFound)
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
		if session == nil {
			next.ServeHTTP(w, r)
			return
		}

		var currentUser database.User
		if err := DB.First(&currentUser, "id = ?", session.UserId).Error; err != nil {
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/integrations/") {
			remainder := strings.TrimPrefix(r.URL.Path, "/integrations/")
			parts := strings.SplitN(remainder, "/", 2)
			integrationName := strings.TrimSpace(parts[0])
			if integrationName != "" {
				visible, visErr := integrations.IsVisibleByName(DB, &currentUser, integrationName)
				if visErr == nil && !visible {
					http.Redirect(w, r, "/chat", http.StatusFound)
					return
				}
			}
		}

		if database.RequireEmailVerificationFromRuntimeConfig() {
			verified, verifyErr := database.IsUserEmailVerified(DB, session.UserId)
			if verifyErr == nil && !verified && r.URL.Path != "/email-verification" {
				http.Redirect(w, r, "/email-verification", http.StatusFound)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
