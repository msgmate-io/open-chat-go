package server

import (
	"backend/api"
	"net/http"
)

func BackendRouting(
	debug bool,
) *http.ServeMux {
	mux := http.NewServeMux()
	v1PublicApis := http.NewServeMux()

	userHandler := &api.UserHandler{}

	if debug {
		v1PublicApis.HandleFunc("POST /test", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Hello, World!"))
		})
	}

	v1PublicApis.HandleFunc("POST /user/login", userHandler.Login)
	v1PublicApis.HandleFunc("POST /user/register", userHandler.Register)

	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", v1PublicApis))

	return mux
}
