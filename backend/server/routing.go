package server

import (
	"backend/api"
	"net/http"
)

func BackendRouting() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, World!"))
	})

	userHandler := &api.UserHandler{}

	mux.HandleFunc("POST /api/v1/user/login", userHandler.Login)
	mux.HandleFunc("POST /api/v1/user/register", userHandler.Register)

	return mux
}
