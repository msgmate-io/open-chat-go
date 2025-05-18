package user

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	BotUUID   *string `json:"bot_uuid,omitempty"`
}

type AccessTokenListItem struct {
	UUID        string     `json:"uuid"`
	Name        string     `json:"name"`
	DisplayName string     `json:"display_name"`
	Scope       string     `json:"scope"`
	BotUUID     *string    `json:"bot_uuid,omitempty"`
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

const botScopedTokenNamePrefix = "bot:"

func composeTokenName(displayName string, botUUID *string) string {
	trimmed := strings.TrimSpace(displayName)
	if botUUID == nil || strings.TrimSpace(*botUUID) == "" {
		return trimmed
	}
	return fmt.Sprintf("%s%s|%s", botScopedTokenNamePrefix, strings.TrimSpace(*botUUID), trimmed)
}

func parseTokenName(rawName string) (displayName string, scope string, botUUID *string) {
	name := strings.TrimSpace(rawName)
	if !strings.HasPrefix(name, botScopedTokenNamePrefix) {
		return name, "account", nil
	}
	payload := strings.TrimPrefix(name, botScopedTokenNamePrefix)
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return name, "account", nil
	}
	trimmedBotUUID := strings.TrimSpace(parts[0])
	if trimmedBotUUID == "" {
		return name, "account", nil
	}
	botUUIDCopy := trimmedBotUUID
	return strings.TrimSpace(parts[1]), "bot", &botUUIDCopy
}

func resolveOwnedBotByUUID(DB *gorm.DB, user *database.User, botUUID string) (database.BotRuntimeConfig, error) {
	if strings.TrimSpace(botUUID) == "" {
		return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
	}

	query := DB.Where("uuid = ?", botUUID)
	if !user.IsAdmin {
		ownedRuntimeIDs := DB.Model(&database.BotRuntimeOwner{}).
			Select("bot_runtime_config_id").
			Where("user_id = ?", user.ID)
		query = query.Where("id IN (?)", ownedRuntimeIDs)
	}

	var runtime database.BotRuntimeConfig
	if err := query.First(&runtime).Error; err != nil {
		return database.BotRuntimeConfig{}, err
	}
	return runtime, nil
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
		permissionsSet[string(database.PermissionCreateBots)] = struct{}{}
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
	trimmedName := strings.TrimSpace(req.Name)
	if trimmedName == "" {
		http.Error(w, "Token name is required", http.StatusBadRequest)
		return
	}

	var botUUID *string
	if req.BotUUID != nil {
		trimmedBotUUID := strings.TrimSpace(*req.BotUUID)
		if trimmedBotUUID != "" {
			if _, err := resolveOwnedBotByUUID(DB, user, trimmedBotUUID); err != nil {
				if err == gorm.ErrRecordNotFound {
					http.Error(w, "Bot not found", http.StatusNotFound)
					return
				}
				http.Error(w, "Failed to resolve bot", http.StatusInternalServerError)
				return
			}
			botUUID = &trimmedBotUUID
		}
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
		Name:        composeTokenName(trimmedName, botUUID),
		TokenPrefix: prefix,
		TokenHash:   tokenHash,
		ExpiresAt:   expiresAt,
	}
	if err := DB.Create(&accessToken).Error; err != nil {
		http.Error(w, "Failed to persist token", http.StatusInternalServerError)
		return
	}

	displayName, scope, parsedBotUUID := parseTokenName(accessToken.Name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AccessTokenCreateResponse{
		Success: true,
		Token:   rawToken,
		AccessToken: AccessTokenListItem{
			UUID:        accessToken.UUID,
			Name:        accessToken.Name,
			DisplayName: displayName,
			Scope:       scope,
			BotUUID:     parsedBotUUID,
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

	botUUIDFilter := strings.TrimSpace(r.URL.Query().Get("bot_uuid"))
	if botUUIDFilter != "" {
		if _, err := resolveOwnedBotByUUID(DB, user, botUUIDFilter); err != nil {
			if err == gorm.ErrRecordNotFound {
				http.Error(w, "Bot not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to resolve bot", http.StatusInternalServerError)
			return
		}
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
	countQuery := DB.Model(&database.AccessToken{}).Where("user_id = ?", user.ID)
	if botUUIDFilter != "" {
		countQuery = countQuery.Where("name LIKE ?", fmt.Sprintf("%s%s|%%", botScopedTokenNamePrefix, botUUIDFilter))
	}
	if err := countQuery.Count(&totalRows).Error; err != nil {
		http.Error(w, "Failed to count access tokens", http.StatusInternalServerError)
		return
	}
	pagination.TotalRows = totalRows
	if pagination.Limit > 0 {
		pagination.TotalPages = int((totalRows + int64(pagination.Limit) - 1) / int64(pagination.Limit))
	}

	var rows []database.AccessToken
	listQuery := DB.Where("user_id = ?", user.ID)
	if botUUIDFilter != "" {
		listQuery = listQuery.Where("name LIKE ?", fmt.Sprintf("%s%s|%%", botScopedTokenNamePrefix, botUUIDFilter))
	}
	if err := listQuery.
		Offset(pagination.GetOffset()).
		Limit(pagination.GetLimit()).
		Order(pagination.GetSort()).
		Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list access tokens", http.StatusInternalServerError)
		return
	}

	items := make([]AccessTokenListItem, 0, len(rows))
	for _, token := range rows {
		displayName, scope, parsedBotUUID := parseTokenName(token.Name)
		items = append(items, AccessTokenListItem{
			UUID:        token.UUID,
			Name:        token.Name,
			DisplayName: displayName,
			Scope:       scope,
			BotUUID:     parsedBotUUID,
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

func (h *UserHandler) RevokeAccessToken(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !requirePermission(DB, user, database.PermissionCreateAPITokens) {
		http.Error(w, "Missing permission: create_api_tokens", http.StatusForbidden)
		return
	}

	tokenUUID := strings.TrimSpace(r.PathValue("token_uuid"))
	if tokenUUID == "" {
		http.Error(w, "token_uuid is required", http.StatusBadRequest)
		return
	}

	var token database.AccessToken
	if err := DB.Where("uuid = ? AND user_id = ?", tokenUUID, user.ID).First(&token).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "Access token not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load access token", http.StatusInternalServerError)
		return
	}

	if token.RevokedAt == nil {
		now := time.Now()
		if err := DB.Model(&token).Update("revoked_at", &now).Error; err != nil {
			http.Error(w, "Failed to revoke access token", http.StatusInternalServerError)
			return
		}
		token.RevokedAt = &now
	}

	displayName, scope, parsedBotUUID := parseTokenName(token.Name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"access_token": AccessTokenListItem{
			UUID:        token.UUID,
			Name:        token.Name,
			DisplayName: displayName,
			Scope:       scope,
			BotUUID:     parsedBotUUID,
			TokenPrefix: token.TokenPrefix,
			CreatedAt:   token.CreatedAt,
			LastUsedAt:  token.LastUsedAt,
			ExpiresAt:   token.ExpiresAt,
			RevokedAt:   token.RevokedAt,
		},
	})
}
