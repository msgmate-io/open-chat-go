package server

import (
	"net/http"
)

func BackendRouting() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, World!"))
	})

	return mux
}
