package models

import (
	"strconv"
	"strings"
)

const (
	// defaultModelsListPageSize is the page size used when the caller does not ask
	// for a specific one.
	defaultModelsListPageSize = 12
	// maxModelsListPageSize bounds the page_size query parameter. It must stay
	// large enough to return the whole model catalogue in a single page: the bot
	// settings create/edit pages request /api/v1/models?page=1&page_size=300 to
	// render the full model dropdown. Oversized values used to be discarded, which
	// silently fell back to the 12 row default and truncated the model picker
	// (ci#1149).
	maxModelsListPageSize = 1000
)

// parseModelsListPageSize resolves the page_size query parameter of the models
// list endpoint. Empty, non-numeric and non-positive values fall back to
// defaultModelsListPageSize; oversized values are clamped to
// maxModelsListPageSize instead of being dropped.
func parseModelsListPageSize(param string) int {
	trimmed := strings.TrimSpace(param)
	if trimmed == "" {
		return defaultModelsListPageSize
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed <= 0 {
		return defaultModelsListPageSize
	}
	if parsed > maxModelsListPageSize {
		return maxModelsListPageSize
	}
	return parsed
}
