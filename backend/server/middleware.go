package server

// Some stuff stolen from 'https://github.com/dreamsofcode-io/nethttp'
import (
	"context"
	"encoding/json"
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

// curl -X POST http://localhost:8080/api/v1/test -H "Content-Type: application/json" -d '{"key1": "value1", "key2": "value2"}'
// TODO: depricate bad practice
func JsonBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			var data map[string]interface{}

			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			ctx := context.WithValue(r.Context(), "json", data)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
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
