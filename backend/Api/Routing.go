package Api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/jwtauth"
	"github.com/swaggo/http-swagger"
)

var tokenAuth *jwtauth.JWTAuth

func SetupRouting(
	serverPort int,
) *chi.Mux {

	tokenAuth = jwtauth.New("HS256", []byte("secret"), nil)

	_, tokenString, _ := tokenAuth.Encode(map[string]interface{}{"user_id": 123})
	fmt.Printf("DEBUG: a sample jwt is %s\n\n", tokenString)

	r := chi.NewRouter()

	// 1 - Register authenticated 'User' routes
	r.Group(func(r chi.Router) {
		r.Use(jwtauth.Verifier(tokenAuth))
		r.Use(jwtauth.Authenticator)

		r.Get("/api/user/self/", UserSelfHandler)
	})

	r.Group(func(r chi.Router) {

		r.Use(middleware.Logger)

		r.Get("/api/schema/", SchemaHandler)

		r.Get("/api/schema/swagger/*", httpSwagger.Handler(
			httpSwagger.URL(fmt.Sprintf("http://localhost:%d/api/schema/", serverPort)),
		))

		r.Post("/api/user/login/", UserLoginHandler)

		r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/html")
			_, err := w.Write([]byte(`
				<h1>Login View</h1>
				<form action="/api/user/login/" method="post">
					<input type="text" name="username" placeholder="username">
					<input type="password" name="password" placeholder="password">
					<button>login</button>
				</form>
			`))

			if err != nil {
				panic(err)
			}
		})

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			// Return html 404
			w.WriteHeader(http.StatusNotFound)
			w.Header().Set("Content-Type", "text/html")
			_, err := w.Write([]byte(`
				<h1>404 Not Found</h1>
				<h2>Open Chat Go API</h2>
				<li>Try <a href="/api/schema/">/api/schema/</a></li>
				<li>Or Interactive <a href="/api/schema/swagger/">/api/schema/swagger/</a></li>
				<li>Or <a href="/user/self/">/user/self/</a></li>
			`))

			if err != nil {
				panic(err)
			}
		})
	})

	return r
}
