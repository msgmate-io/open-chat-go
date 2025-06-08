package server

import (
	"backend/database"
	"backend/runtimecfg"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultMobileAPICacheTTLSeconds = 600
	defaultMobileAPICacheMaxBody    = 524288
	defaultMobileAPICacheMaxRows    = 1000
)

type mobileProxyCacheConfig struct {
	Enabled      bool
	TTL          time.Duration
	MaxBodyBytes int
	MaxRows      int
}

func resolveMobileProxyCacheConfig(target *url.URL) mobileProxyCacheConfig {
	values := runtimecfg.GetAll()

	if !strings.EqualFold(strings.TrimSpace(values["MOBILE_ROUTE_API_WS_TO_UPSTREAM"].Value), "true") {
		return mobileProxyCacheConfig{}
	}

	if isLoopbackTarget(target) {
		return mobileProxyCacheConfig{}
	}

	enabled := parseRuntimeBool(values["MOBILE_API_CACHE_ENABLED"].Value, false)
	if !enabled {
		return mobileProxyCacheConfig{}
	}

	ttlSeconds := parseRuntimeInt(values["MOBILE_API_CACHE_TTL_SECONDS"].Value, defaultMobileAPICacheTTLSeconds)
	if ttlSeconds <= 0 {
		ttlSeconds = defaultMobileAPICacheTTLSeconds
	}

	maxBodyBytes := parseRuntimeInt(values["MOBILE_API_CACHE_MAX_BODY_BYTES"].Value, defaultMobileAPICacheMaxBody)
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMobileAPICacheMaxBody
	}

	maxRows := parseRuntimeInt(values["MOBILE_API_CACHE_MAX_ROWS"].Value, defaultMobileAPICacheMaxRows)
	if maxRows <= 0 {
		maxRows = defaultMobileAPICacheMaxRows
	}

	return mobileProxyCacheConfig{
		Enabled:      true,
		TTL:          time.Duration(ttlSeconds) * time.Second,
		MaxBodyBytes: maxBodyBytes,
		MaxRows:      maxRows,
	}
}

func parseRuntimeBool(raw string, fallback bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseRuntimeInt(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func isLoopbackTarget(target *url.URL) bool {
	if target == nil {
		return false
	}

	host := strings.TrimSpace(target.Hostname())
	if host == "" {
		return false
	}

	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func mobileProxyRequestCacheable(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodGet {
		return false
	}
	path := strings.TrimSpace(r.URL.Path)
	return strings.HasPrefix(path, "/api/")
}

func storeMobileProxyCacheResponse(resp *http.Response, cfg mobileProxyCacheConfig) {
	if resp == nil || resp.Request == nil {
		return
	}

	if !mobileProxyResponseCacheable(resp, cfg) {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	if len(body) > cfg.MaxBodyBytes {
		return
	}

	db := mobileProxyDBFromRequest(resp.Request)
	if db == nil {
		return
	}

	now := time.Now().UTC()
	cacheKey, sessionHash, requestPath := mobileProxyCacheIdentity(resp.Request)
	headersJSON, err := json.Marshal(mobileProxyResponseHeaders(resp.Header))
	if err != nil {
		return
	}

	entry := database.MobileAPIResponseCache{
		CacheKey:            cacheKey,
		Method:              resp.Request.Method,
		URLPathWithQuery:    requestPath,
		SessionScopeHash:    sessionHash,
		StatusCode:          resp.StatusCode,
		ContentType:         strings.TrimSpace(resp.Header.Get("Content-Type")),
		ResponseHeadersJSON: string(headersJSON),
		ResponseBody:        body,
		ExpiresAt:           now.Add(cfg.TTL),
		LastValidatedAt:     now,
	}

	_ = db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cache_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"method":                entry.Method,
			"url_path_with_query":   entry.URLPathWithQuery,
			"session_scope_hash":    entry.SessionScopeHash,
			"status_code":           entry.StatusCode,
			"content_type":          entry.ContentType,
			"response_headers_json": entry.ResponseHeadersJSON,
			"response_body":         entry.ResponseBody,
			"expires_at":            entry.ExpiresAt,
			"last_validated_at":     entry.LastValidatedAt,
		}),
	}).Create(&entry).Error

	pruneMobileProxyCache(db, cfg.MaxRows)
}

func mobileProxyResponseCacheable(resp *http.Response, cfg mobileProxyCacheConfig) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !strings.Contains(contentType, "application/json") {
		return false
	}

	cacheControl := strings.ToLower(strings.TrimSpace(resp.Header.Get("Cache-Control")))
	if strings.Contains(cacheControl, "no-store") {
		return false
	}

	if len(resp.Header.Values("Set-Cookie")) > 0 {
		return false
	}

	if resp.ContentLength > int64(cfg.MaxBodyBytes) {
		return false
	}

	return true
}

func mobileProxyDBFromRequest(r *http.Request) *gorm.DB {
	if r == nil {
		return nil
	}
	db, _ := r.Context().Value("db").(*gorm.DB)
	return db
}

func mobileProxyCacheIdentity(r *http.Request) (cacheKey string, sessionHash string, requestPath string) {
	path := strings.TrimSpace(r.URL.Path)
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		path += "?" + rawQuery
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	sessionToken := ""
	if cookie, err := r.Cookie("session_id"); err == nil {
		sessionToken = strings.TrimSpace(cookie.Value)
	}

	sessionScopeInput := authHeader + "|" + sessionToken
	sessionScopeHash := hashString(sessionScopeInput)
	keyMaterial := strings.ToUpper(r.Method) + "|" + path + "|" + sessionScopeHash

	return hashString(keyMaterial), sessionScopeHash, path
}

func hashString(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func mobileProxyResponseHeaders(header http.Header) map[string]string {
	copyHeaders := map[string]string{}
	for _, key := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified"} {
		value := strings.TrimSpace(header.Get(key))
		if value != "" {
			copyHeaders[key] = value
		}
	}
	return copyHeaders
}

func pruneMobileProxyCache(db *gorm.DB, maxRows int) {
	if db == nil || maxRows <= 0 {
		return
	}

	var count int64
	if err := db.Model(&database.MobileAPIResponseCache{}).Count(&count).Error; err != nil {
		return
	}
	if count <= int64(maxRows) {
		return
	}

	overflow := count - int64(maxRows)
	if overflow <= 0 {
		return
	}

	var ids []uint
	if err := db.Model(&database.MobileAPIResponseCache{}).
		Order("last_validated_at asc").
		Limit(int(overflow)).
		Pluck("id", &ids).Error; err != nil {
		return
	}
	if len(ids) == 0 {
		return
	}

	_ = db.Delete(&database.MobileAPIResponseCache{}, ids).Error
}

func tryServeMobileProxyCachedResponse(w http.ResponseWriter, r *http.Request) bool {
	db := mobileProxyDBFromRequest(r)
	if db == nil {
		return false
	}

	cacheKey, _, _ := mobileProxyCacheIdentity(r)
	var entry database.MobileAPIResponseCache
	err := db.Where("cache_key = ?", cacheKey).First(&entry).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false
		}
		return false
	}

	now := time.Now().UTC()
	if entry.ExpiresAt.Before(now) {
		_ = db.Delete(&database.MobileAPIResponseCache{}, entry.ID).Error
		return false
	}

	headers := map[string]string{}
	if entry.ResponseHeadersJSON != "" {
		_ = json.Unmarshal([]byte(entry.ResponseHeadersJSON), &headers)
	}
	for key, value := range headers {
		if strings.EqualFold(key, "Set-Cookie") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		w.Header().Set(key, value)
	}
	if contentType := strings.TrimSpace(entry.ContentType); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	entry.LastValidatedAt = now
	_ = db.Model(&database.MobileAPIResponseCache{}).
		Where("id = ?", entry.ID).
		Update("last_validated_at", now).Error

	w.Header().Set("X-OpenChat-Cache", "offline-fallback")
	w.Header().Set("X-OpenChat-Cache-Expires-At", entry.ExpiresAt.UTC().Format(time.RFC3339))
	w.WriteHeader(entry.StatusCode)
	_, _ = w.Write(entry.ResponseBody)
	return true
}
