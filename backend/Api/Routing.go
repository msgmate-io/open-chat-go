package Api

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/swaggo/http-swagger"
	"net/http"
)

func SetupRouting() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Get("/api/schema/", SchemaHandler)

	r.Get("/api/schema/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("http://localhost:3000/api/schema/"), //The url pointing to API definition
	))

	r.Get("/hello", UserSelfHandler)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("welcome"))
	})

	return r
}
