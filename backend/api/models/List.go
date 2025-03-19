package models

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
)

type ModelListItem struct {
	ID          uint            `json:"id"`
	ModelID     string          `json:"model_id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	Config      json.RawMessage `json:"configuration"`
}

type ModelsListResponse struct {
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalRows  int64           `json:"total_rows"`
	TotalPages int             `json:"total_pages"`
	Rows       []ModelListItem `json:"rows"`
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

	var totalRows int64
	if err := DB.Model(&database.ModelConfig{}).Count(&totalRows).Error; err != nil {
		http.Error(w, "Failed to count models", http.StatusInternalServerError)
		return
	}

	totalPages := 0
	if totalRows > 0 {
		totalPages = int((totalRows + int64(pageSize) - 1) / int64(pageSize))
	}
	if totalPages > 0 && page > totalPages {
		page = totalPages
	}

	var configs []database.ModelConfig
	query := DB.Order("is_default DESC").Order("model_id ASC")
	if totalRows > 0 {
		query = query.Offset((page - 1) * pageSize).Limit(pageSize)
	}
	if err := query.Find(&configs).Error; err != nil {
		http.Error(w, "Failed to list models", http.StatusInternalServerError)
		return
	}

	rows := make([]ModelListItem, 0, len(configs))
	for _, cfg := range configs {
		rows = append(rows, ModelListItem{
			ID:          cfg.ID,
			ModelID:     cfg.ModelID,
			Title:       cfg.Title,
			Description: cfg.Description,
			IsDefault:   cfg.IsDefault,
			Config:      cfg.Configuration,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ModelsListResponse{
		Page:       page,
		PageSize:   pageSize,
		TotalRows:  totalRows,
		TotalPages: totalPages,
		Rows:       rows,
	})
}
