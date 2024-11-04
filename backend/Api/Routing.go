package Api

import (
	"backend/Views"
	"fmt"
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

		r.Get("/login", Views.LoginView)

		r.Get("/", Views.Page404View)
	})

	return r
}
