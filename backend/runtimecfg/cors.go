package runtimecfg

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseCORSAllowedOrigins parses a comma separated origin allowlist. Entries
// must be absolute origins (scheme://host[:port]); wildcards are rejected.
func ParseCORSAllowedOrigins(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	origins := []string{}
	for _, entry := range strings.Split(trimmed, ",") {
		origin := strings.TrimSpace(entry)
		if origin == "" {
			continue
		}
		if strings.Contains(origin, "*") {
			return nil, fmt.Errorf("wildcard CORS origins are not allowed: %q", origin)
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid CORS origin %q: expected scheme://host[:port]", origin)
		}
		origins = append(origins, strings.TrimRight(strings.ToLower(origin), "/"))
	}
	return origins, nil
}

// CORSAllowedOrigins returns the configured cross-origin allowlist
// (CORS_ALLOWED_ORIGINS). Invalid configuration falls back to an empty
// allowlist, which disables cross-origin access.
func CORSAllowedOrigins() []string {
	origins, err := ParseCORSAllowedOrigins(GetAll()["CORS_ALLOWED_ORIGINS"].Value)
	if err != nil {
		return nil
	}
	return origins
}

// PublicBaseURL returns the canonical public origin of this Open Chat
// instance (PUBLIC_BASE_URL), used when advertising the API base URL to
// external clients.
func PublicBaseURL() string {
	return strings.TrimSpace(GetAll()["PUBLIC_BASE_URL"].Value)
}
