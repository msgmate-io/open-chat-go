package server

// Some stuff stolen from 'https://github.com/dreamsofcode-io/nethttp'
import (
	"backend/database"
	"context"
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

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		log.Println(cookie)
		if err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		token := strings.TrimSpace(cookie.Value)

		var session database.Session
		if err := database.DB.First(&session, "token = ?", token).Error; err != nil {

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
		if err := database.DB.First(&user, "id = ?", session.UserId).Error; err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
