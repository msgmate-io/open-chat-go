package api

import (
	"net/http"
)

type Handler struct{}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("User Created"))
}

func (h *Handler) FindByID(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement
}
