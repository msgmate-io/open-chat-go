package user

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"
)

const maxUserAccessTokens = 5

type PermissionsResponse struct {
	Rows []string `json:"rows"`
}

type CreateAccessTokenRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

type AccessTokenListItem struct {
	UUID        string     `json:"uuid"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type AccessTokenCreateResponse struct {
	Success     bool                `json:"success"`
	Token       string              `json:"token"`
	AccessToken AccessTokenListItem `json:"access_token"`
}

type AccessTokensListResponse struct {
	database.Pagination
	Rows []AccessTokenListItem `json:"rows"`
}

func hasPermission(DB *gorm.DB, user *database.User, permission database.PermissionName) bool {
	if user.IsAdmin {
		return true
	}
	var userPermission database.Permission
	q := DB.First(&userPermission, "user_id = ? AND permission = ?", user.ID, permission)
	return q.Error == nil
}

func requirePermission(DB *gorm.DB, user *database.User, permission database.PermissionName) bool {
	return hasPermission(DB, user, permission)
}

func (h *UserHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	permissionsSet := map[string]struct{}{}
	if user.IsAdmin {
		permissionsSet[string(database.PermissionCreateAPITokens)] = struct{}{}
	}

	var rows []database.Permission
	if err := DB.Where("user_id = ?", user.ID).Find(&rows).Error; err == nil {
		for _, row := range rows {
			permissionsSet[string(row.Permission)] = struct{}{}
		}
	}

	permissions := make([]string, 0, len(permissionsSet))
	for permission := range permissionsSet {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PermissionsResponse{Rows: permissions})
}

func (h *UserHandler) CreateAccessToken(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !requirePermission(DB, user, database.PermissionCreateAPITokens) {
		http.Error(w, "Missing permission: create_api_tokens", http.StatusForbidden)
		return
	}

	var req CreateAccessTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "Token name is required", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		var activeCount int64
		now := time.Now()
		DB.Model(&database.AccessToken{}).
			Where("user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", user.ID, now).
			Count(&activeCount)
		if activeCount >= maxUserAccessTokens {
			http.Error(w, fmt.Sprintf("Token limit reached (max %d for regular users)", maxUserAccessTokens), http.StatusConflict)
			return
		}
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			http.Error(w, "Invalid expires_at format (RFC3339 required)", http.StatusBadRequest)
			return
		}
		expiresAt = &parsed
	}

	rawToken, prefix, tokenHash, err := database.GenerateRawAccessToken()
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	accessToken := database.AccessToken{
		UserId:      user.ID,
		Name:        req.Name,
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
		ExpiresAt:   expiresAt,
	}
	if err := DB.Create(&accessToken).Error; err != nil {
		http.Error(w, "Failed to persist token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AccessTokenCreateResponse{
		Success: true,
		Token:   rawToken,
		AccessToken: AccessTokenListItem{
			UUID:        accessToken.UUID,
			Name:        accessToken.Name,
			TokenPrefix: accessToken.TokenPrefix,
			CreatedAt:   accessToken.CreatedAt,
			LastUsedAt:  accessToken.LastUsedAt,
			ExpiresAt:   accessToken.ExpiresAt,
			RevokedAt:   accessToken.RevokedAt,
		},
	})
}

func (h *UserHandler) ListAccessTokens(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !requirePermission(DB, user, database.PermissionCreateAPITokens) {
		http.Error(w, "Missing permission: create_api_tokens", http.StatusForbidden)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 20}
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

	var totalRows int64
	if err := DB.Model(&database.AccessToken{}).Where("user_id = ?", user.ID).Count(&totalRows).Error; err != nil {
		http.Error(w, "Failed to count access tokens", http.StatusInternalServerError)
		return
	}
	pagination.TotalRows = totalRows
	if pagination.Limit > 0 {
		pagination.TotalPages = int((totalRows + int64(pagination.Limit) - 1) / int64(pagination.Limit))
	}

	var rows []database.AccessToken
	if err := DB.Where("user_id = ?", user.ID).
		Offset(pagination.GetOffset()).
		Limit(pagination.GetLimit()).
		Order(pagination.GetSort()).
		Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list access tokens", http.StatusInternalServerError)
		return
	}

	items := make([]AccessTokenListItem, 0, len(rows))
	for _, token := range rows {
		items = append(items, AccessTokenListItem{
			UUID:        token.UUID,
			Name:        token.Name,
			TokenPrefix: token.TokenPrefix,
			CreatedAt:   token.CreatedAt,
			LastUsedAt:  token.LastUsedAt,
			ExpiresAt:   token.ExpiresAt,
			RevokedAt:   token.RevokedAt,
		})
	}

	pagination.Rows = items
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
