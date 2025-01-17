package tls

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
)

type PaginatedKeysResponse struct {
	database.Pagination
	Rows []database.Key `json:"rows"`
}

func ListKeys(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 10}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	var keys []database.Key
	q := DB.Scopes(database.Paginate(&keys, &pagination, DB)).Find(&keys)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(keys) == 0 && pagination.Page > 1 {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}

	response := PaginatedKeysResponse{
		Pagination: pagination,
		Rows:       keys,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
