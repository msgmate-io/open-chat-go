package admin

import (
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// This file exposes the token/cache usage overview (ci#910). The OpenRouter
// credit burn is dominated by prompt tokens, so an operator needs one place
// that answers "who is spending, how much of it is prompt vs completion, and
// are we actually getting prompt-cache hits?" without querying the database by
// hand.

// tokenUsageRow is one sender's aggregated token usage over the window.
type tokenUsageRow struct {
	Bot              string `json:"bot"`
	Messages         int64  `json:"messages"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	// CacheHitRatio is cached/prompt over the window (0 when nothing was cached
	// or the provider does not report caching).
	CacheHitRatio float64 `json:"cache_hit_ratio"`
}

type tokenUsageOverviewResponse struct {
	GeneratedAt string          `json:"generated_at"`
	WindowHours int             `json:"window_hours"`
	Rows        []tokenUsageRow `json:"rows"`
	Totals      tokenUsageRow   `json:"totals"`
}

// @doc:token-usage-overview
// GetTokenUsageOverview reports prompt/completion/cached token totals per
// sender over a rolling window (default 24h), so prompt-cache regressions and
// cost-spiking bots are observable. Admin only; carries no provider secrets.
func GetTokenUsageOverview(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	windowHours := 24
	if value, convErr := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("hours"))); convErr == nil && value > 0 {
		windowHours = value
	}
	if windowHours > 24*90 {
		windowHours = 24 * 90
	}

	query := tokenUsageAggregateQuery(DB.Dialector.Name(), windowHours)
	var rows []tokenUsageRow
	if err := DB.Raw(query).Scan(&rows).Error; err != nil {
		http.Error(w, "Failed to aggregate token usage", http.StatusInternalServerError)
		return
	}
	totals := tokenUsageRow{Bot: "TOTAL"}
	for _, row := range rows {
		totals.Messages += row.Messages
		totals.PromptTokens += row.PromptTokens
		totals.CompletionTokens += row.CompletionTokens
		totals.CachedTokens += row.CachedTokens
	}
	if totals.PromptTokens > 0 {
		totals.CacheHitRatio = float64(totals.CachedTokens) / float64(totals.PromptTokens)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenUsageOverviewResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		WindowHours: windowHours,
		Rows:        rows,
		Totals:      totals,
	})
}

// tokenUsageAggregateQuery builds the per-sender aggregation for the backend in
// use. Both SQLite (json_extract) and PostgreSQL (->/->>) are supported; the
// ratio is computed in Go so the SQL stays portable.
func tokenUsageAggregateQuery(dialect string, windowHours int) string {
	if dialect == "postgres" {
		return fmt.Sprintf(`
SELECT u.username AS bot,
  count(*) AS messages,
  COALESCE(SUM((m.meta_data->'token_usage'->>'prompt_tokens')::bigint),0) AS prompt_tokens,
  COALESCE(SUM((m.meta_data->'token_usage'->>'completion_tokens')::bigint),0) AS completion_tokens,
  COALESCE(SUM((m.meta_data->'token_usage'->'prompt_tokens_details'->>'cached_tokens')::bigint),0) AS cached_tokens,
  0 AS cache_hit_ratio
FROM messages m
JOIN users u ON u.id = m.sender_id
WHERE m.meta_data ? 'token_usage' AND m.created_at > now() - interval '%d hours'
GROUP BY u.username
ORDER BY prompt_tokens DESC`, windowHours)
	}
	return fmt.Sprintf(`
SELECT u.username AS bot,
  count(*) AS messages,
  COALESCE(SUM(json_extract(m.meta_data,'$.token_usage.prompt_tokens')),0) AS prompt_tokens,
  COALESCE(SUM(json_extract(m.meta_data,'$.token_usage.completion_tokens')),0) AS completion_tokens,
  COALESCE(SUM(json_extract(m.meta_data,'$.token_usage.prompt_tokens_details.cached_tokens')),0) AS cached_tokens,
  0 AS cache_hit_ratio
FROM messages m
JOIN users u ON u.id = m.sender_id
WHERE json_extract(m.meta_data,'$.token_usage') IS NOT NULL
  AND m.created_at > datetime('now', '-%d hours')
GROUP BY u.username
ORDER BY prompt_tokens DESC`, windowHours)
}
