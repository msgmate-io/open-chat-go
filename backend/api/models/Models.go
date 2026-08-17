package models

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

type botOption struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type ModelListItem struct {
	UUID          string          `json:"uuid"`
	ModelID       string          `json:"model_id"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Hoster        string          `json:"hoster"`
	Source        string          `json:"source"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
	IsDefault     bool            `json:"is_default"`
	IsPublic      bool            `json:"is_public"`
	IsOwned       bool            `json:"is_owned"`
	OwnerUserID   *uint           `json:"owner_user_id,omitempty"`
	Bots          []string        `json:"bots,omitempty"`
}

type modelsFilters struct {
	Hosters []string    `json:"hosters"`
	Sources []string    `json:"sources"`
	Bots    []botOption `json:"bots,omitempty"`
}

type ModelsListResponse struct {
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalRows  int64           `json:"total_rows"`
	TotalPages int             `json:"total_pages"`
	Rows       []ModelListItem `json:"rows"`
	Filters    modelsFilters   `json:"filters"`
}

type ModelUpsertRequest struct {
	ModelID       string          `json:"model_id"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Configuration json.RawMessage `json:"configuration"`
	IsPublic      bool            `json:"is_public"`
}

type ModelPatchRequest struct {
	ModelID       *string          `json:"model_id,omitempty"`
	Title         *string          `json:"title,omitempty"`
	Description   *string          `json:"description,omitempty"`
	Configuration *json.RawMessage `json:"configuration,omitempty"`
	IsPublic      *bool            `json:"is_public,omitempty"`
}

func parseHoster(configuration json.RawMessage) string {
	var cfg map[string]interface{}
	if err := json.Unmarshal(configuration, &cfg); err != nil {
		return ""
	}
	backend, ok := cfg["backend"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(backend)
}

func parseSource(modelID string) string {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

func canReadModel(cfg database.ModelConfig, user *database.User) bool {
	if user != nil && user.IsAdmin {
		return true
	}
	if cfg.IsPublic {
		return true
	}
	if user == nil || cfg.OwnerUserId == nil {
		return false
	}
	return *cfg.OwnerUserId == user.ID
}

func canWriteModel(cfg database.ModelConfig, user *database.User) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin {
		return true
	}
	if cfg.OwnerUserId == nil {
		return false
	}
	return *cfg.OwnerUserId == user.ID
}

func toModelListItem(cfg database.ModelConfig, user *database.User) ModelListItem {
	isOwned := user != nil && cfg.OwnerUserId != nil && *cfg.OwnerUserId == user.ID
	item := ModelListItem{
		UUID:        cfg.UUID,
		ModelID:     cfg.ModelID,
		Title:       cfg.Title,
		Description: cfg.Description,
		Hoster:      parseHoster(cfg.Configuration),
		Source:      parseSource(cfg.ModelID),
		IsDefault:   cfg.IsDefault,
		IsPublic:    cfg.IsPublic,
		IsOwned:     isOwned,
	}
	if user != nil && user.IsAdmin {
		item.OwnerUserID = cfg.OwnerUserId
		item.Bots = append([]string(nil), cfg.BotUsernames...)
		sort.Strings(item.Bots)
	}
	item.Configuration = cfg.Configuration
	return item
}

func validateConfiguration(modelID string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("configuration is required")
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, errors.New("configuration must be a valid JSON object")
	}
	if cfg == nil {
		return nil, errors.New("configuration must be a valid JSON object")
	}
	if existingModel, ok := cfg["model"].(string); ok {
		existingModel = strings.TrimSpace(existingModel)
		if existingModel != "" && existingModel != modelID {
			return nil, errors.New("configuration.model must match model_id")
		}
	}
	cfg["model"] = modelID
	normalized, err := json.Marshal(cfg)
	if err != nil {
		return nil, errors.New("configuration must be serializable")
	}
	return normalized, nil
}

func resolveReadableModelByUUID(db *gorm.DB, user *database.User, modelUUID string) (*database.ModelConfig, error) {
	var cfg database.ModelConfig
	if err := db.Where("uuid = ?", modelUUID).First(&cfg).Error; err != nil {
		return nil, err
	}
	if !canReadModel(cfg, user) {
		return nil, gorm.ErrRecordNotFound
	}
	return &cfg, nil
}

// List returns model catalog rows visible to the current caller.
//
//	@Summary		List models
//	@Description	List public models and caller-owned models. Admin users see all rows.
//	@Tags			models
//	@Produce		json
//	@Param			page query int false "Page number" minimum(1)
//	@Param			page_size query int false "Page size" minimum(1) maximum(100)
//	@Param			hoster query string false "Filter by backend hoster"
//	@Param			source query string false "Filter by model source prefix"
//	@Param			q query string false "Search by title/model/description"
//	@Param			bot query string false "Admin only: filter by assigned bot name"
//	@Param			bot_uuid query string false "Admin only: filter by assigned bot UUID"
//	@Success		200 {object} ModelsListResponse
//	@Router			/api/v1/models [get]
func (h *ModelsHandler) List(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	user, _ := r.Context().Value("user").(*database.User)
	isAdmin := user != nil && user.IsAdmin

	page := 1
	pageSize := 12
	if pageParam := strings.TrimSpace(r.URL.Query().Get("page")); pageParam != "" {
		if parsed, parseErr := strconv.Atoi(pageParam); parseErr == nil && parsed > 0 {
			page = parsed
		}
	}
	if limitParam := strings.TrimSpace(r.URL.Query().Get("page_size")); limitParam != "" {
		if parsed, parseErr := strconv.Atoi(limitParam); parseErr == nil && parsed > 0 && parsed <= 100 {
			pageSize = parsed
		}
	}

	hosterFilter := strings.TrimSpace(r.URL.Query().Get("hoster"))
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))
	queryFilter := strings.TrimSpace(r.URL.Query().Get("q"))
	botFilter := strings.TrimSpace(r.URL.Query().Get("bot"))
	botUUIDFilter := strings.TrimSpace(r.URL.Query().Get("bot_uuid"))

	query := DB.Model(&database.ModelConfig{}).Order("is_default DESC").Order("model_id ASC")
	if user == nil {
		query = query.Where("is_public = ?", true)
	} else if !isAdmin {
		query = query.Where("is_public = ? OR owner_user_id = ?", true, user.ID)
	}
	if sourceFilter != "" {
		query = query.Where("model_id LIKE ?", sourceFilter+"/%")
	}
	if queryFilter != "" {
		like := "%" + strings.ToLower(queryFilter) + "%"
		query = query.Where("LOWER(title) LIKE ? OR LOWER(model_id) LIKE ? OR LOWER(description) LIKE ?", like, like, like)
	}
	if isAdmin && botFilter != "" {
		query = query.Where("CAST(bot_usernames AS TEXT) LIKE ?", "%\""+botFilter+"\"%")
	}

	var modelConfigs []database.ModelConfig
	if err := query.Find(&modelConfigs).Error; err != nil {
		http.Error(w, "Failed to list models", http.StatusInternalServerError)
		return
	}

	if isAdmin && botUUIDFilter != "" {
		var bot database.User
		if err := DB.Where("uuid = ? AND is_automated = ?", botUUIDFilter, true).First(&bot).Error; err == nil {
			filtered := make([]database.ModelConfig, 0, len(modelConfigs))
			for _, cfg := range modelConfigs {
				if cfg.AssignedToBot(bot.Name) {
					filtered = append(filtered, cfg)
				}
			}
			modelConfigs = filtered
		}
	}

	if hosterFilter != "" {
		normalizedHosterFilter := strings.ToLower(hosterFilter)
		filtered := make([]database.ModelConfig, 0, len(modelConfigs))
		for _, cfg := range modelConfigs {
			if strings.ToLower(parseHoster(cfg.Configuration)) == normalizedHosterFilter {
				filtered = append(filtered, cfg)
			}
		}
		modelConfigs = filtered
	}

	totalRows := int64(len(modelConfigs))
	totalPages := 0
	if totalRows > 0 {
		totalPages = int((totalRows + int64(pageSize) - 1) / int64(pageSize))
	}
	offset := (page - 1) * pageSize
	if offset > len(modelConfigs) {
		offset = len(modelConfigs)
	}
	end := offset + pageSize
	if end > len(modelConfigs) {
		end = len(modelConfigs)
	}
	paged := modelConfigs[offset:end]

	hostersSet := map[string]struct{}{}
	sourcesSet := map[string]struct{}{}
	for _, cfg := range modelConfigs {
		if hoster := parseHoster(cfg.Configuration); hoster != "" {
			hostersSet[hoster] = struct{}{}
		}
		if source := parseSource(cfg.ModelID); source != "" {
			sourcesSet[source] = struct{}{}
		}
	}
	hosters := make([]string, 0, len(hostersSet))
	for hoster := range hostersSet {
		hosters = append(hosters, hoster)
	}
	sort.Strings(hosters)
	sources := make([]string, 0, len(sourcesSet))
	for source := range sourcesSet {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	rows := make([]ModelListItem, 0, len(paged))
	for _, cfg := range paged {
		rows = append(rows, toModelListItem(cfg, user))
	}

	filters := modelsFilters{Hosters: hosters, Sources: sources}
	if isAdmin {
		var botUsers []database.User
		if err := DB.Where("is_automated = ?", true).Find(&botUsers).Error; err == nil {
			filters.Bots = make([]botOption, 0, len(botUsers))
			for _, bot := range botUsers {
				filters.Bots = append(filters.Bots, botOption{UUID: bot.UUID, Name: bot.Name})
			}
			sort.Slice(filters.Bots, func(i, j int) bool {
				if filters.Bots[i].Name == filters.Bots[j].Name {
					return filters.Bots[i].UUID < filters.Bots[j].UUID
				}
				return filters.Bots[i].Name < filters.Bots[j].Name
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ModelsListResponse{
		Page:       page,
		PageSize:   pageSize,
		TotalRows:  totalRows,
		TotalPages: totalPages,
		Rows:       rows,
		Filters:    filters,
	})
}

// Get returns one model catalog row by UUID.
//
//	@Summary		Get model
//	@Description	Retrieve one model row by UUID if visible to the caller.
//	@Tags			models
//	@Produce		json
//	@Param			model_uuid path string true "Model UUID"
//	@Success		200 {object} ModelListItem
//	@Failure		404 {string} string "Model not found"
//	@Router			/api/v1/models/{model_uuid} [get]
func (h *ModelsHandler) Get(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}
	modelUUID := strings.TrimSpace(r.PathValue("model_uuid"))
	if modelUUID == "" {
		http.Error(w, "Model not found", http.StatusNotFound)
		return
	}
	user, _ := r.Context().Value("user").(*database.User)
	cfg, err := resolveReadableModelByUUID(DB, user, modelUUID)
	if err != nil {
		http.Error(w, "Model not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toModelListItem(*cfg, user))
}

// Create creates a new user-owned model row.
//
//	@Summary		Create model
//	@Description	Create a user-owned model configuration row.
//	@Tags			models
//	@Accept			json
//	@Produce		json
//	@Param			request body ModelUpsertRequest true "Model create payload"
//	@Success		201 {object} ModelListItem
//	@Failure		400 {string} string "Invalid payload"
//	@Router			/api/v1/models [post]
func (h *ModelsHandler) Create(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	var req ModelUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	if req.ModelID == "" {
		http.Error(w, "model_id is required", http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		req.Title = req.ModelID
	}
	normalizedConfig, err := validateConfiguration(req.ModelID, req.Configuration)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ownerID := user.ID
	record := database.ModelConfig{
		OwnerUserId:   &ownerID,
		Title:         req.Title,
		Description:   req.Description,
		ModelID:       req.ModelID,
		Configuration: normalizedConfig,
		BotUsernames:  database.StringSliceJSON{},
		IsPublic:      req.IsPublic,
		IsDefault:     false,
	}
	if err := DB.Create(&record).Error; err != nil {
		http.Error(w, "Failed to create model", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toModelListItem(record, user))
}

// Patch updates an existing model row by UUID.
//
//	@Summary		Patch model
//	@Description	Update a model row by UUID. Owner or admin only.
//	@Tags			models
//	@Accept			json
//	@Produce		json
//	@Param			model_uuid path string true "Model UUID"
//	@Param			request body ModelPatchRequest true "Model patch payload"
//	@Success		200 {object} ModelListItem
//	@Failure		400 {string} string "Invalid payload"
//	@Failure		403 {string} string "Forbidden"
//	@Failure		404 {string} string "Model not found"
//	@Router			/api/v1/models/{model_uuid} [patch]
func (h *ModelsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	modelUUID := strings.TrimSpace(r.PathValue("model_uuid"))
	if modelUUID == "" {
		http.Error(w, "Model not found", http.StatusNotFound)
		return
	}

	var record database.ModelConfig
	if err := DB.Where("uuid = ?", modelUUID).First(&record).Error; err != nil {
		http.Error(w, "Model not found", http.StatusNotFound)
		return
	}
	if !canWriteModel(record, user) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req ModelPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	updates := map[string]interface{}{}
	targetModelID := record.ModelID
	if req.ModelID != nil {
		trimmed := strings.TrimSpace(*req.ModelID)
		if trimmed == "" {
			http.Error(w, "model_id cannot be empty", http.StatusBadRequest)
			return
		}
		targetModelID = trimmed
		updates["model_id"] = trimmed
	}
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		if trimmed == "" {
			http.Error(w, "title cannot be empty", http.StatusBadRequest)
			return
		}
		updates["title"] = trimmed
	}
	if req.Description != nil {
		updates["description"] = strings.TrimSpace(*req.Description)
	}
	if req.IsPublic != nil {
		updates["is_public"] = *req.IsPublic
	}
	if req.Configuration != nil {
		normalizedConfig, err := validateConfiguration(targetModelID, *req.Configuration)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		updates["configuration"] = normalizedConfig
	} else if req.ModelID != nil {
		http.Error(w, "configuration is required when changing model_id", http.StatusBadRequest)
		return
	}
	if len(updates) == 0 {
		http.Error(w, "No updates provided", http.StatusBadRequest)
		return
	}

	if err := DB.Model(&record).Updates(updates).Error; err != nil {
		http.Error(w, "Failed to update model", http.StatusInternalServerError)
		return
	}
	if err := DB.Where("id = ?", record.ID).First(&record).Error; err != nil {
		http.Error(w, "Failed to reload model", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toModelListItem(record, user))
}

// Delete removes an existing model row by UUID.
//
//	@Summary		Delete model
//	@Description	Delete a model row by UUID. Owner or admin only.
//	@Tags			models
//	@Param			model_uuid path string true "Model UUID"
//	@Success		204
//	@Failure		403 {string} string "Forbidden"
//	@Failure		404 {string} string "Model not found"
//	@Router			/api/v1/models/{model_uuid} [delete]
func (h *ModelsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	modelUUID := strings.TrimSpace(r.PathValue("model_uuid"))
	if modelUUID == "" {
		http.Error(w, "Model not found", http.StatusNotFound)
		return
	}

	var record database.ModelConfig
	if err := DB.Where("uuid = ?", modelUUID).First(&record).Error; err != nil {
		http.Error(w, "Model not found", http.StatusNotFound)
		return
	}
	if !canWriteModel(record, user) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := DB.Delete(&record).Error; err != nil {
		http.Error(w, "Failed to delete model", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
