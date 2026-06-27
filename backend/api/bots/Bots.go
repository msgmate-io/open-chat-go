package bots

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var errAmbiguousIdentifier = errors.New("ambiguous bot identifier")

type BotDTO struct {
	UUID                string                 `json:"uuid"`
	BotUserUUID         string                 `json:"bot_user_uuid"`
	BotUsername         string                 `json:"bot_username"`
	BotContactToken     string                 `json:"bot_contact_token"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	DefaultSharedConfig map[string]interface{} `json:"default_shared_config"`
	IsPublic            bool                   `json:"is_public"`
	IsActive            bool                   `json:"is_active"`
}

type ListedBotsPage struct {
	Limit      int      `json:"limit"`
	Page       int      `json:"page"`
	TotalPages int      `json:"total_pages"`
	Rows       []BotDTO `json:"rows"`
}

type CreateBotRequest struct {
	Name                string                 `json:"name"`
	Description         string                 `json:"description,omitempty"`
	DefaultSharedConfig map[string]interface{} `json:"default_shared_config"`
	Password            string                 `json:"password,omitempty"`
	IsPublic            bool                   `json:"is_public,omitempty"`
}

type CreateBotResponse struct {
	Bot               BotDTO  `json:"bot"`
	GeneratedPassword *string `json:"generated_password,omitempty"`
}

type UpdateBotRequest struct {
	Name                *string                `json:"name,omitempty"`
	Description         *string                `json:"description,omitempty"`
	DefaultSharedConfig map[string]interface{} `json:"default_shared_config,omitempty"`
	IsPublic            *bool                  `json:"is_public,omitempty"`
	IsActive            *bool                  `json:"is_active,omitempty"`
}

type CreateBotInteractionRequest struct {
	Message         string                 `json:"message"`
	ToolInit        map[string]interface{} `json:"tool_init,omitempty"`
	ConfigOverrides map[string]interface{} `json:"config_overrides,omitempty"`
	AutoShare       bool                   `json:"auto_share,omitempty"`
}

type BotInteractionChatShare struct {
	ChatUUID      string `json:"chat_uuid"`
	ChatShareUUID string `json:"chat_share_uuid"`
}

type BotInteractionResponse struct {
	ChatUUID             string                   `json:"chat_uuid"`
	ChatShareUUID        string                   `json:"chat_share_uuid,omitempty"`
	ChatShare            *BotInteractionChatShare `json:"chat_share,omitempty"`
	SharedInteractionURL string                   `json:"shared_interaction_url,omitempty"`
}

func requestBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if commaIdx := strings.Index(host, ","); commaIdx >= 0 {
		host = strings.TrimSpace(host[:commaIdx])
	}
	if host == "" {
		return ""
	}

	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}

	return scheme + "://" + host
}

func ensureOwnedChatShare(DB *gorm.DB, chatID uint, owningUserID uint) (database.SharedChatInstance, error) {
	var share database.SharedChatInstance
	err := DB.Where("chat_id = ? AND owning_user_id = ?", chatID, owningUserID).First(&share).Error
	if err == nil {
		return share, nil
	}
	if err != gorm.ErrRecordNotFound {
		return database.SharedChatInstance{}, err
	}

	share = database.SharedChatInstance{
		ChatId:        chatID,
		OwningUserId:  owningUserID,
		ChatShareUUID: uuid.NewString(),
	}
	if err := DB.Create(&share).Error; err != nil {
		return database.SharedChatInstance{}, err
	}

	return share, nil
}

func hasPermission(DB *gorm.DB, user *database.User, permission database.PermissionName) bool {
	if user.IsAdmin {
		return true
	}
	var userPermission database.Permission
	q := DB.First(&userPermission, "user_id = ? AND permission = ?", user.ID, permission)
	return q.Error == nil
}

func decodeSharedConfig(raw []byte) map[string]interface{} {
	result := map[string]interface{}{}
	if len(raw) == 0 {
		return result
	}
	_ = json.Unmarshal(raw, &result)
	return result
}

func toDTO(runtime database.BotRuntimeConfig) BotDTO {
	return BotDTO{
		UUID:                runtime.UUID,
		BotUserUUID:         runtime.BotUser.UUID,
		BotUsername:         runtime.BotUser.Name,
		BotContactToken:     runtime.BotUser.ContactToken,
		Name:                runtime.Name,
		Description:         runtime.Description,
		DefaultSharedConfig: decodeSharedConfig(runtime.DefaultSharedConfig),
		IsPublic:            runtime.IsPublic,
		IsActive:            runtime.IsActive,
	}
}

func getString(config map[string]interface{}, key string) (string, bool) {
	raw, ok := config[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func getNumber(config map[string]interface{}, key string) (float64, bool) {
	raw, ok := config[key]
	if !ok {
		return 0, false
	}
	value, ok := raw.(float64)
	if !ok {
		return 0, false
	}
	return value, true
}

func validateStringArray(config map[string]interface{}, key string) error {
	raw, exists := config[key]
	if !exists {
		return nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("%s must be an array of strings", key)
	}
	for idx, item := range items {
		value, ok := item.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s[%d] must be a non-empty string", key, idx)
		}
	}
	return nil
}

func validateSharedConfigStructure(config map[string]interface{}) error {
	if config == nil {
		return fmt.Errorf("default_shared_config is required")
	}

	model, ok := getString(config, "model")
	if !ok || model == "" {
		return fmt.Errorf("default_shared_config.model is required and must be a non-empty string")
	}

	backend, ok := getString(config, "backend")
	if !ok || backend == "" {
		return fmt.Errorf("default_shared_config.backend is required and must be a non-empty string")
	}

	for _, key := range []string{"endpoint", "system_prompt"} {
		if _, exists := config[key]; exists {
			if value, isString := config[key].(string); !isString || strings.TrimSpace(value) == "" {
				return fmt.Errorf("default_shared_config.%s must be a non-empty string when provided", key)
			}
		}
	}

	for _, key := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty"} {
		if value, exists := getNumber(config, key); exists {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("default_shared_config.%s must be a finite number", key)
			}
		} else if _, provided := config[key]; provided {
			return fmt.Errorf("default_shared_config.%s must be a number", key)
		}
	}

	for _, key := range []string{"max_tokens", "context"} {
		if value, exists := getNumber(config, key); exists {
			if value < 1 || math.Trunc(value) != value {
				return fmt.Errorf("default_shared_config.%s must be a positive integer", key)
			}
		} else if _, provided := config[key]; provided {
			return fmt.Errorf("default_shared_config.%s must be a number", key)
		}
	}

	if raw, exists := config["reasoning"]; exists {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("default_shared_config.reasoning must be a boolean")
		}
	}

	if raw, exists := config["tool_init"]; exists {
		if _, ok := raw.(map[string]interface{}); !ok {
			return fmt.Errorf("default_shared_config.tool_init must be an object")
		}
	}

	if err := validateStringArray(config, "tools"); err != nil {
		return err
	}
	if err := msgmate.ValidateToolsAndInitConfig(config["tools"], config["tool_init"]); err != nil {
		return fmt.Errorf("default_shared_config invalid tools/tool_init: %w", err)
	}

	return nil
}

func resolveByBotUsername(DB *gorm.DB, user *database.User, username string) (database.BotRuntimeConfig, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
	}

	baseQuery := DB.Model(&database.BotRuntimeConfig{}).
		Preload("BotUser").
		Joins("JOIN users ON users.id = bot_runtime_configs.bot_user_id").
		Where("users.name = ? AND bot_runtime_configs.is_active = ?", username, true)

	if user.IsAdmin {
		var matches []database.BotRuntimeConfig
		if err := baseQuery.Find(&matches).Error; err != nil {
			return database.BotRuntimeConfig{}, err
		}
		if len(matches) == 0 {
			return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
		}
		if len(matches) > 1 {
			return database.BotRuntimeConfig{}, errAmbiguousIdentifier
		}
		return matches[0], nil
	}

	var runtime database.BotRuntimeConfig
	if err := baseQuery.Where("bot_runtime_configs.owner_user_id = ?", user.ID).First(&runtime).Error; err != nil {
		return database.BotRuntimeConfig{}, err
	}
	return runtime, nil
}

func parsePagination(r *http.Request, defaultLimit int) (int, int) {
	page := 1
	limit := defaultLimit
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if parsedPage, err := strconv.Atoi(pageParam); err == nil && parsedPage > 0 {
			page = parsedPage
		}
	}
	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if parsedLimit, err := strconv.Atoi(limitParam); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	return page, limit
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func resolveReadableBot(DB *gorm.DB, user *database.User, identifier string) (database.BotRuntimeConfig, error) {
	if identifier == "" {
		return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
	}

	var runtime database.BotRuntimeConfig
	if err := DB.Preload("BotUser").Where("uuid = ? AND is_active = ?", identifier, true).First(&runtime).Error; err == nil {
		if runtime.OwnerUserId != user.ID && !user.IsAdmin && !runtime.IsPublic {
			return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
		}
		return runtime, nil
	}

	query := DB.Preload("BotUser").Where("owner_user_id = ? AND name = ? AND is_active = ?", user.ID, identifier, true)
	if user.IsAdmin {
		var matches []database.BotRuntimeConfig
		if err := DB.Preload("BotUser").Where("name = ? AND is_active = ?", identifier, true).Find(&matches).Error; err != nil {
			return database.BotRuntimeConfig{}, err
		}
		if len(matches) == 0 {
			return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
		}
		if len(matches) > 1 {
			return database.BotRuntimeConfig{}, errAmbiguousIdentifier
		}
		return matches[0], nil
	}
	if err := query.First(&runtime).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return database.BotRuntimeConfig{}, err
		}
		return resolveByBotUsername(DB, user, identifier)
	}
	return runtime, nil
}

func resolveOwnedBot(DB *gorm.DB, user *database.User, identifier string) (database.BotRuntimeConfig, error) {
	runtime, err := resolveReadableBot(DB, user, identifier)
	if err != nil {
		return database.BotRuntimeConfig{}, err
	}
	if runtime.OwnerUserId != user.ID && !user.IsAdmin {
		return database.BotRuntimeConfig{}, gorm.ErrRecordNotFound
	}
	return runtime, nil
}

func ensureContactAndDirectChat(DB *gorm.DB, owner database.User, botUser database.User) error {
	contact := database.Contact{
		OwningUserId:  owner.ID,
		ContactUserId: botUser.ID,
		ContactToken:  botUser.ContactToken,
	}
	if err := DB.Where("owning_user_id = ? AND contact_user_id = ?", owner.ID, botUser.ID).FirstOrCreate(&contact).Error; err != nil {
		return err
	}

	var chat database.Chat
	err := DB.Where(
		"(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
		owner.ID,
		botUser.ID,
		botUser.ID,
		owner.ID,
	).First(&chat).Error
	if err == nil {
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	if owner.ID < botUser.ID {
		chat = database.Chat{User1Id: owner.ID, User2Id: botUser.ID}
	} else {
		chat = database.Chat{User1Id: botUser.ID, User2Id: owner.ID}
	}
	return DB.Create(&chat).Error
}

func randomPassword() (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = chars[int(buf[i])%len(chars)]
	}
	return string(buf), nil
}

// Create bot
// @Summary      Create bot
// @Description  Create an owner-scoped automated bot user with default runtime config
// @Tags         bots
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        request body bots.CreateBotRequest true "Bot creation request"
// @Success      200 {object} bots.CreateBotResponse
// @Failure      400 {string} string "Invalid request"
// @Failure      403 {string} string "Missing permission"
// @Failure      409 {string} string "Bot name already exists for owner"
// @Router       /api/v1/bots [post]
func (h *BotsHandler) Create(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !hasPermission(DB, user, database.PermissionCreateBots) {
		http.Error(w, "Missing permission: create_bots", http.StatusForbidden)
		return
	}

	var req CreateBotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.DefaultSharedConfig == nil {
		http.Error(w, "default_shared_config is required", http.StatusBadRequest)
		return
	}
	if err := validateSharedConfigStructure(req.DefaultSharedConfig); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	password := req.Password
	var generatedPassword *string
	if strings.TrimSpace(password) == "" {
		generated, err := randomPassword()
		if err != nil {
			http.Error(w, "Failed to generate password", http.StatusInternalServerError)
			return
		}
		password = generated
		generatedPassword = &generated
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	configJSON, err := json.Marshal(req.DefaultSharedConfig)
	if err != nil {
		http.Error(w, "default_shared_config must be a valid JSON object", http.StatusBadRequest)
		return
	}

	var runtime database.BotRuntimeConfig
	err = DB.Transaction(func(tx *gorm.DB) error {
		botUser := database.User{
			Name:         req.Name,
			Email:        fmt.Sprintf("bot-%s@bot.local", uuid.NewString()),
			PasswordHash: string(hashedPassword),
			ContactToken: uuid.NewString(),
			IsAutomated:  true,
		}
		if err := tx.Create(&botUser).Error; err != nil {
			return err
		}

		isPublic := false
		if user.IsAdmin {
			isPublic = req.IsPublic
		}

		runtime = database.BotRuntimeConfig{
			BotUserId:           botUser.ID,
			OwnerUserId:         user.ID,
			Name:                req.Name,
			Description:         req.Description,
			DefaultSharedConfig: configJSON,
			IsPublic:            isPublic,
			IsActive:            true,
		}
		if err := tx.Create(&runtime).Error; err != nil {
			return err
		}

		if err := ensureContactAndDirectChat(tx, *user, botUser); err != nil {
			return err
		}

		return tx.Preload("BotUser").First(&runtime, runtime.ID).Error
	})
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "idx_bot_owner_name") || strings.Contains(errText, "duplicate") {
			http.Error(w, "bot name already exists for owner", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create bot", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CreateBotResponse{Bot: toDTO(runtime), GeneratedPassword: generatedPassword})
}

// List bots
// @Summary      List bots
// @Description  List owner bots, optionally including public bots from other owners
// @Tags         bots
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        page query int false "Page number" default(1)
// @Param        limit query int false "Page size" default(40)
// @Param        include_public query bool false "Include public bots"
// @Success      200 {object} bots.ListedBotsPage
// @Router       /api/v1/bots/list [get]
func (h *BotsHandler) List(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	page, limit := parsePagination(r, 40)
	includePublic := parseBoolQuery(r.URL.Query().Get("include_public"))

	query := DB.Model(&database.BotRuntimeConfig{}).Where("is_active = ? AND owner_user_id = ?", true, user.ID)
	if includePublic {
		query = query.Or("is_active = ? AND is_public = ? AND owner_user_id <> ?", true, true, user.ID)
	}

	var totalRows int64
	if err := query.Count(&totalRows).Error; err != nil {
		http.Error(w, "Failed to count bots", http.StatusInternalServerError)
		return
	}

	totalPages := 0
	if limit > 0 {
		totalPages = int((totalRows + int64(limit) - 1) / int64(limit))
	}

	var rows []database.BotRuntimeConfig
	if err := query.Preload("BotUser").
		Offset((page - 1) * limit).
		Limit(limit).
		Order("id desc").
		Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list bots", http.StatusInternalServerError)
		return
	}

	items := make([]BotDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDTO(row))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListedBotsPage{Limit: limit, Page: page, TotalPages: totalPages, Rows: items})
}

// Get bot
// @Summary      Get bot
// @Description  Get a bot by UUID or owner-scoped name
// @Tags         bots
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        identifier path string true "Bot UUID or owner-scoped name"
// @Success      200 {object} bots.BotDTO
// @Failure      404 {string} string "Bot not found"
// @Router       /api/v1/bots/{identifier} [get]
func (h *BotsHandler) Get(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	runtime, err := resolveReadableBot(DB, user, identifier)
	if err != nil {
		if errors.Is(err, errAmbiguousIdentifier) {
			http.Error(w, "ambiguous bot identifier", http.StatusConflict)
			return
		}
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toDTO(runtime))
}

// Update bot
// @Summary      Update bot
// @Description  Update owner bot metadata and runtime defaults
// @Tags         bots
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        identifier path string true "Bot UUID or owner-scoped name"
// @Param        request body bots.UpdateBotRequest true "Bot patch request"
// @Success      200 {object} bots.BotDTO
// @Failure      404 {string} string "Bot not found"
// @Router       /api/v1/bots/{identifier} [patch]
func (h *BotsHandler) Update(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	runtime, err := resolveOwnedBot(DB, user, identifier)
	if err != nil {
		if errors.Is(err, errAmbiguousIdentifier) {
			http.Error(w, "ambiguous bot identifier", http.StatusConflict)
			return
		}
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	var req UpdateBotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.DefaultSharedConfig != nil {
		if err := validateSharedConfigStructure(req.DefaultSharedConfig); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{}
		if req.Name != nil {
			name := strings.TrimSpace(*req.Name)
			if name == "" {
				return fmt.Errorf("name cannot be empty")
			}
			updates["name"] = name
			if err := tx.Model(&database.User{}).Where("id = ?", runtime.BotUserId).Update("name", name).Error; err != nil {
				return err
			}
		}
		if req.Description != nil {
			updates["description"] = *req.Description
		}
		if req.DefaultSharedConfig != nil {
			configJSON, err := json.Marshal(req.DefaultSharedConfig)
			if err != nil {
				return err
			}
			updates["default_shared_config"] = configJSON
		}
		if req.IsPublic != nil {
			updates["is_public"] = *req.IsPublic
		}
		if req.IsActive != nil {
			updates["is_active"] = *req.IsActive
		}
		if len(updates) > 0 {
			if err := tx.Model(&runtime).Updates(updates).Error; err != nil {
				return err
			}
		}
		return tx.Preload("BotUser").First(&runtime, runtime.ID).Error
	})
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "name cannot be empty") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.Contains(errText, "idx_bot_owner_name") || strings.Contains(errText, "duplicate") {
			http.Error(w, "bot name already exists for owner", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to update bot", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toDTO(runtime))
}

// Save bot config
// @Summary      Save bot config
// @Description  Replace bot default_shared_config after strict structure validation
// @Tags         bots
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        identifier path string true "Bot UUID, owner-scoped name, or bot username"
// @Param        request body map[string]interface{} true "Full default_shared_config JSON"
// @Success      200 {object} bots.BotDTO
// @Failure      400 {string} string "Invalid config"
// @Failure      404 {string} string "Bot not found"
// @Router       /api/v1/bots/{identifier}/config [put]
func (h *BotsHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	runtime, err := resolveOwnedBot(DB, user, identifier)
	if err != nil {
		if errors.Is(err, errAmbiguousIdentifier) {
			http.Error(w, "ambiguous bot identifier", http.StatusConflict)
			return
		}
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	config := map[string]interface{}{}
	if err := decoder.Decode(&config); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validateSharedConfigStructure(config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		http.Error(w, "Failed to encode config", http.StatusBadRequest)
		return
	}

	if err := DB.Model(&runtime).Update("default_shared_config", configJSON).Error; err != nil {
		http.Error(w, "Failed to save bot config", http.StatusInternalServerError)
		return
	}
	if err := DB.Preload("BotUser").First(&runtime, runtime.ID).Error; err != nil {
		http.Error(w, "Failed to load bot", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toDTO(runtime))
}

// Delete bot
// @Summary      Delete bot
// @Description  Soft-disable a bot
// @Tags         bots
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        identifier path string true "Bot UUID or owner-scoped name"
// @Success      200 {object} bots.BotDTO
// @Failure      404 {string} string "Bot not found"
// @Router       /api/v1/bots/{identifier} [delete]
func (h *BotsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	runtime, err := resolveOwnedBot(DB, user, identifier)
	if err != nil {
		if errors.Is(err, errAmbiguousIdentifier) {
			http.Error(w, "ambiguous bot identifier", http.StatusConflict)
			return
		}
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	if err := DB.Model(&runtime).Update("is_active", false).Error; err != nil {
		http.Error(w, "Failed to delete bot", http.StatusInternalServerError)
		return
	}
	runtime.IsActive = false

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toDTO(runtime))
}

// Create bot interaction
// @Summary      Create bot interaction
// @Description  Create an interaction chat for the specified bot using default config + overrides
// @Tags         bots
// @Accept       json
// @Produce      json
// @Security     SessionAuth
// @Param        identifier path string true "Bot UUID or owner-scoped name"
// @Param        request body bots.CreateBotInteractionRequest true "Interaction request"
// @Success      200 {object} bots.BotInteractionResponse
// @Failure      404 {string} string "Bot not found"
// @Router       /api/v1/bots/{identifier}/interactions [post]
func (h *BotsHandler) CreateInteraction(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	queueClient, clientErr := util.GetAsynqClient(r)
	queueInspector, inspectorErr := util.GetAsynqInspector(r)
	if clientErr != nil || inspectorErr != nil {
		http.Error(w, "Unable to access async queue", http.StatusInternalServerError)
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	runtime, err := resolveReadableBot(DB, user, identifier)
	if err != nil {
		if errors.Is(err, errAmbiguousIdentifier) {
			http.Error(w, "ambiguous bot identifier", http.StatusConflict)
			return
		}
		http.Error(w, "Bot not found", http.StatusNotFound)
		return
	}

	var req CreateBotInteractionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	effectiveConfig := decodeSharedConfig(runtime.DefaultSharedConfig)
	for k, v := range req.ConfigOverrides {
		effectiveConfig[k] = v
	}
	effectiveConfig["tool_init"] = req.ToolInit
	if err := msgmate.ValidateToolsAndInitConfig(effectiveConfig["tools"], effectiveConfig["tool_init"]); err != nil {
		http.Error(w, fmt.Sprintf("invalid tools/tool_init: %v", err), http.StatusBadRequest)
		return
	}
	configJSON, err := json.Marshal(effectiveConfig)
	if err != nil {
		http.Error(w, "Failed to process config", http.StatusBadRequest)
		return
	}

	var chat database.Chat
	var message database.Message
	var share database.SharedChatInstance
	err = DB.Transaction(func(tx *gorm.DB) error {
		if user.ID < runtime.BotUserId {
			chat = database.Chat{User1Id: user.ID, User2Id: runtime.BotUserId, ChatType: "interaction"}
		} else {
			chat = database.Chat{User1Id: runtime.BotUserId, User2Id: user.ID, ChatType: "interaction"}
		}
		if err := tx.Create(&chat).Error; err != nil {
			return err
		}

		sharedConfig := database.SharedChatConfig{ChatId: chat.ID, ConfigData: configJSON}
		if err := tx.Create(&sharedConfig).Error; err != nil {
			return err
		}
		if err := tx.Model(&chat).Update("shared_config_id", sharedConfig.ID).Error; err != nil {
			return err
		}

		message = database.Message{
			ChatId:     chat.ID,
			SenderId:   user.ID,
			ReceiverId: runtime.BotUserId,
			Text:       &req.Message,
		}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		if err := tx.Model(&chat).Update("latest_message_id", message.ID).Error; err != nil {
			return err
		}

		if req.AutoShare {
			createdShare, shareErr := ensureOwnedChatShare(tx, chat.ID, user.ID)
			if shareErr != nil {
				return shareErr
			}
			share = createdShare
		}
		return nil
	})
	if err != nil {
		http.Error(w, "Failed to create interaction", http.StatusInternalServerError)
		return
	}

	if _, enqueueErr := workqueue.EnqueueBotReply(queueClient, queueInspector, workqueue.BotReplyPayload{
		ChatUUID:    chat.UUID,
		MessageUUID: message.UUID,
		BotUserID:   runtime.BotUserId,
	}); enqueueErr != nil {
		http.Error(w, "Failed to enqueue bot reply", http.StatusInternalServerError)
		return
	}

	response := BotInteractionResponse{ChatUUID: chat.UUID}
	if req.AutoShare {
		response.ChatShareUUID = share.ChatShareUUID
		response.ChatShare = &BotInteractionChatShare{
			ChatUUID:      chat.UUID,
			ChatShareUUID: share.ChatShareUUID,
		}
		baseURL := requestBaseURL(r)
		if baseURL != "" {
			response.SharedInteractionURL = baseURL + "/interaction/" + share.ChatShareUUID
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
