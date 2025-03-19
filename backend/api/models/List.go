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
	IsDefault   bool            `json:"is_default"`
	Config      json.RawMessage `json:"configuration"`
	Hoster      string          `json:"hoster"`
	Source      string          `json:"source"`
	Bots        []string        `json:"bots"`
}

type modelsFilters struct {
	Hosters []string    `json:"hosters"`
	Sources []string    `json:"sources"`
	Bots    []botOption `json:"bots"`
}

type ModelsListResponse struct {
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalRows  int64           `json:"total_rows"`
	TotalPages int             `json:"total_pages"`
	Rows       []ModelListItem `json:"rows"`
	Filters    modelsFilters   `json:"filters"`
}

func (h *ModelsHandler) List(w http.ResponseWriter, r *http.Request) {
	DB, _, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	page := 1
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if parsed, parseErr := strconv.Atoi(pageParam); parseErr == nil && parsed > 0 {
			page = parsed
		}
	}

	pageSize := 12
	if pageSizeParam := r.URL.Query().Get("page_size"); pageSizeParam != "" {
		if parsed, parseErr := strconv.Atoi(pageSizeParam); parseErr == nil && parsed > 0 && parsed <= 100 {
			pageSize = parsed
		}
	}

	hosterFilter := strings.TrimSpace(r.URL.Query().Get("hoster"))
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))
	botFilter := strings.TrimSpace(r.URL.Query().Get("bot"))
	botUUIDFilter := strings.TrimSpace(r.URL.Query().Get("bot_uuid"))

	var configs []database.ModelConfig
	if err := DB.Order("is_default DESC").Order("model_id ASC").Find(&configs).Error; err != nil {
		http.Error(w, "Failed to list models", http.StatusInternalServerError)
		return
	}

	var botUsers []database.User
	if err := DB.Where("is_automated = ?", true).Find(&botUsers).Error; err != nil {
		http.Error(w, "Failed to load bots", http.StatusInternalServerError)
		return
	}

	botUUIDByName := make(map[string]string, len(botUsers))
	allBots := make([]botOption, 0, len(botUsers))
	for _, bot := range botUsers {
		botUUIDByName[bot.Name] = bot.UUID
		allBots = append(allBots, botOption{UUID: bot.UUID, Name: bot.Name})
	}

	var filterBotName string
	if botUUIDFilter != "" {
		for _, bot := range botUsers {
			if bot.UUID == botUUIDFilter {
				filterBotName = bot.Name
				break
			}
		}
	}

	hostersSet := map[string]struct{}{}
	sourcesSet := map[string]struct{}{}
	rows := make([]ModelListItem, 0, len(configs))
	for _, cfg := range configs {
		source := ""
		if parts := strings.SplitN(cfg.ModelID, "/", 2); len(parts) == 2 {
			source = parts[0]
		}
		if source != "" {
			sourcesSet[source] = struct{}{}
		}

		hoster := ""
		var cfgMap map[string]interface{}
		if err := json.Unmarshal(cfg.Configuration, &cfgMap); err == nil {
			if backendValue, ok := cfgMap["backend"].(string); ok {
				hoster = strings.TrimSpace(backendValue)
			}
		}
		if hoster != "" {
			hostersSet[hoster] = struct{}{}
		}

		bots := append([]string(nil), cfg.BotUsernames...)
		sort.Strings(bots)

		if hosterFilter != "" && !strings.EqualFold(hosterFilter, hoster) {
			continue
		}
		if sourceFilter != "" && !strings.EqualFold(sourceFilter, source) {
			continue
		}
		if botFilter != "" {
			matchedBot := false
			for _, botName := range bots {
				if strings.EqualFold(botFilter, botName) {
					matchedBot = true
					break
				}
			}
			if !matchedBot {
				continue
			}
		}
		if filterBotName != "" && !cfg.AssignedToBot(filterBotName) {
			continue
		}

		rows = append(rows, ModelListItem{
			ID:          cfg.ID,
			ModelID:     cfg.ModelID,
			Title:       cfg.Title,
			Description: cfg.Description,
			IsDefault:   cfg.IsDefault,
			Config:      cfg.Configuration,
			Hoster:      hoster,
			Source:      source,
			Bots:        bots,
		})
	}

	totalRows := int64(len(rows))
	totalPages := 0
	if totalRows > 0 {
		totalPages = int((totalRows + int64(pageSize) - 1) / int64(pageSize))
	}
	if totalPages > 0 && page > totalPages {
		page = totalPages
	}

	start := 0
	if page > 1 {
		start = (page - 1) * pageSize
	}
	if start > len(rows) {
		start = len(rows)
	}
	end := start + pageSize
	if end > len(rows) {
		end = len(rows)
	}
	rows = rows[start:end]

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

	sort.Slice(allBots, func(i, j int) bool {
		if allBots[i].Name == allBots[j].Name {
			return allBots[i].UUID < allBots[j].UUID
		}
		return allBots[i].Name < allBots[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ModelsListResponse{
		Page:       page,
		PageSize:   pageSize,
		TotalRows:  totalRows,
		TotalPages: totalPages,
		Rows:       rows,
		Filters: modelsFilters{
			Hosters: hosters,
			Sources: sources,
			Bots:    allBots,
		},
	})
}
