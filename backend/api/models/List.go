package models

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type botOption struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type ModelListItem struct {
	ID          uint            `json:"id"`
	ModelID     string          `json:"model_id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Hoster      string          `json:"hoster"`
	Source      string          `json:"source"`
	Config      json.RawMessage `json:"configuration,omitempty"`
	Bots        []string        `json:"bots,omitempty"`
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

// List returns the platform model catalog for both guests and authenticated users.
//
//	@Summary		List models
//	@Description	List model configurations with pagination and filters. Guests receive restricted fields, admins receive extended metadata.
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
//	@Router			/api/v1/models/list [get]
func (h *ModelsHandler) List(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	user, _ := r.Context().Value("user").(*database.User)
	isAdmin := user != nil && user.IsAdmin

	pagination := database.Pagination{Page: 1, Limit: 12}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, parseErr := strconv.Atoi(pageParam); parseErr == nil && page > 0 {
			pagination.Page = page
		}
	}
	if limitParam := r.URL.Query().Get("page_size"); limitParam != "" {
		if limit, parseErr := strconv.Atoi(limitParam); parseErr == nil && limit > 0 && limit <= 100 {
			pagination.Limit = limit
		}
	}

	hosterFilter := strings.TrimSpace(r.URL.Query().Get("hoster"))
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))
	queryFilter := strings.TrimSpace(r.URL.Query().Get("q"))
	botFilter := strings.TrimSpace(r.URL.Query().Get("bot"))
	botUUIDFilter := strings.TrimSpace(r.URL.Query().Get("bot_uuid"))

	var modelConfigs []database.ModelConfig
	allQuery := DB.Model(&database.ModelConfig{}).Order("is_default DESC").Order("model_id ASC")
	if sourceFilter != "" {
		allQuery = allQuery.Where("model_id LIKE ?", sourceFilter+"/%")
	}
	if queryFilter != "" {
		like := "%" + strings.ToLower(queryFilter) + "%"
		allQuery = allQuery.Where("LOWER(title) LIKE ? OR LOWER(model_id) LIKE ? OR LOWER(description) LIKE ?", like, like, like)
	}
	if hosterFilter != "" {
		allQuery = allQuery.Where("CAST(configuration AS TEXT) LIKE ?", "%\"backend\":\""+hosterFilter+"\"%")
	}
	if isAdmin && botFilter != "" {
		allQuery = allQuery.Where("CAST(bot_usernames AS TEXT) LIKE ?", "%\""+botFilter+"\"%")
	}

	if err := allQuery.Find(&modelConfigs).Error; err != nil {
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

	pagination.TotalRows = int64(len(modelConfigs))
	if pagination.TotalRows > 0 {
		pagination.TotalPages = int((pagination.TotalRows + int64(pagination.Limit) - 1) / int64(pagination.Limit))
	}
	offset := pagination.GetOffset()
	end := offset + pagination.GetLimit()
	if offset > len(modelConfigs) {
		offset = len(modelConfigs)
	}
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
		item := ModelListItem{
			ID:          cfg.ID,
			ModelID:     cfg.ModelID,
			Title:       cfg.Title,
			Description: cfg.Description,
			Hoster:      parseHoster(cfg.Configuration),
			Source:      parseSource(cfg.ModelID),
		}
		if isAdmin {
			item.Config = cfg.Configuration
			item.Bots = append([]string(nil), cfg.BotUsernames...)
			sort.Strings(item.Bots)
		}
		rows = append(rows, item)
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
	json.NewEncoder(w).Encode(ModelsListResponse{
		Page:       pagination.Page,
		PageSize:   pagination.Limit,
		TotalRows:  pagination.TotalRows,
		TotalPages: pagination.TotalPages,
		Rows:       rows,
		Filters:    filters,
	})
}
