package tls

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
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

func ListKeyNames(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var keyNames []string
	DB.Model(&database.Key{}).Pluck("key_name", &keyNames)

	// Log the key names for debugging
	fmt.Println("Key Names:", keyNames)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(keyNames)
}

type CreateKeyRequest struct {
	Sealed     bool   `json:"sealed"`
	KeyName    string `json:"key_name"`
	KeyType    string `json:"key_type"`
	KeyContent []byte `json:"key_content"`
}

func CreateKey(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var request CreateKeyRequest
	err = json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	DB.Create(&database.Key{
		Sealed:     request.Sealed,
		KeyName:    request.KeyName,
		KeyType:    request.KeyType,
		KeyContent: request.KeyContent,
	})

	w.WriteHeader(http.StatusCreated)
}

// keys/<key-name/get
func RetrieveKey(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	keyName := r.PathValue("key_name")

	var key database.Key
	DB.Where("key_name = ?", keyName).First(&key)

	if key.KeyName == "" {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(key)
}
