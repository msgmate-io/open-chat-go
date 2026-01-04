// Package webtools provides shared, agent-facing web helpers: a readable
// fetch (HTML-to-text with a bounded size) and a keyless web search. It backs
// the core web_fetch/web_search built-in tools as well as the msgmate coder's
// coder_webfetch/coder_websearch tools, so every agent uses one implementation.
package webtools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultMaxBytes bounds how much of a fetched document is decoded.
	DefaultMaxBytes = 512 * 1024
	// DefaultSearchLimit is the number of search results returned by default.
	DefaultSearchLimit = 8
	// MaxSearchLimit caps the requested number of search results.
	MaxSearchLimit = 20

	fetchUserAgent = "Mozilla/5.0 (compatible; OpenChatBot/1.0; +https://msgmate.io)"
)

// FetchResult is a readable view of a fetched document.
type FetchResult struct {
	URL         string `json:"url"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Title       string `json:"title,omitempty"`
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// SearchResult is a single web search hit.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

var (
	scriptStyleBlock = regexp.MustCompile(`(?is)<(script|style|noscript|template|svg|head)\b[^>]*>.*?</(script|style|noscript|template|svg|head)>`)
	htmlComment      = regexp.MustCompile(`(?s)<!--.*?-->`)
	titleTag         = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	blockTag         = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|tr|table|section|article|header|footer|h[1-6]|blockquote|pre|hr)\b[^>]*>`)
	anyTag           = regexp.MustCompile(`(?s)<[^>]+>`)
	whitespaceRun    = regexp.MustCompile(`[ \t\f\v]+`)
	allWhitespace    = regexp.MustCompile(`\s+`)
	blankLineRun     = regexp.MustCompile(`\n{3,}`)
	ddgResultLink    = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippet       = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	htmlTagAny       = regexp.MustCompile(`(?s)<[^>]+>`)
)

// Fetch retrieves a URL and returns a bounded, readable text representation.
// HTML documents are converted to plain text; other text content is returned
// as-is. maxBytes <= 0 uses DefaultMaxBytes.
func Fetch(ctx context.Context, rawURL string, maxBytes int) (FetchResult, error) {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return FetchResult{}, fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return FetchResult{}, err
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.5")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return FetchResult{}, err
	}
	truncated := len(body) > maxBytes
	if truncated {
		body = body[:maxBytes]
	}

	contentType := resp.Header.Get("Content-Type")
	result := FetchResult{
		URL:         target,
		Status:      resp.StatusCode,
		ContentType: contentType,
		Truncated:   truncated,
	}
	if isHTMLContentType(contentType, body) {
		result.Title, result.Content = htmlToText(string(body))
	} else {
		result.Content = string(body)
	}
	return result, nil
}

// Search runs a web search and returns the top results. It uses a
// SearXNG-compatible JSON endpoint when OPENCHAT_WEB_SEARCH_URL is configured
// and otherwise falls back to the keyless DuckDuckGo HTML endpoint.
func Search(ctx context.Context, query string, limit int, site string) ([]SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("query is required")
	}
	if site = strings.TrimSpace(site); site != "" {
		q = q + " site:" + site
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	if base := strings.TrimSpace(os.Getenv("OPENCHAT_WEB_SEARCH_URL")); base != "" {
		return searchSearx(ctx, base, q, limit)
	}
	return searchDuckDuckGo(ctx, q, limit)
}

func searchSearx(ctx context.Context, base, query string, limit int) ([]SearchResult, error) {
	endpoint := strings.TrimRight(base, "/") + "/search"
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("search endpoint returned %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, DefaultMaxBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid search response: %w", err)
	}
	out := make([]SearchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, SearchResult{Title: strings.TrimSpace(r.Title), URL: strings.TrimSpace(r.URL), Snippet: strings.TrimSpace(r.Content)})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// searchDuckDuckGo parses the keyless DuckDuckGo HTML endpoint. It is a
// best-effort fallback used when no search backend is configured.
func searchDuckDuckGo(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("q", query)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://html.duckduckgo.com/html/", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("search returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, DefaultMaxBytes))
	if err != nil {
		return nil, err
	}
	page := string(body)

	links := ddgResultLink.FindAllStringSubmatch(page, -1)
	snippets := ddgSnippet.FindAllStringSubmatch(page, -1)
	out := make([]SearchResult, 0, len(links))
	for i, m := range links {
		href := strings.TrimSpace(html.UnescapeString(m[1]))
		title := stripTags(m[2])
		if strings.HasPrefix(href, "//duckduckgo.com/l/?uddg=") || strings.Contains(href, "uddg=") {
			href = unwrapDuckDuckGo(href)
		}
		if href == "" || title == "" {
			continue
		}
		snippet := ""
		if i < len(snippets) {
			snippet = stripTags(snippets[i][1])
		}
		out = append(out, SearchResult{Title: title, URL: href, Snippet: snippet})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func unwrapDuckDuckGo(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return href
	}
	if target := parsed.Query().Get("uddg"); target != "" {
		return target
	}
	return href
}

func isHTMLContentType(contentType string, body []byte) bool {
	lower := strings.ToLower(contentType)
	if strings.Contains(lower, "text/html") || strings.Contains(lower, "xhtml") {
		return true
	}
	if strings.Contains(lower, "application/json") || strings.Contains(lower, "text/plain") ||
		strings.Contains(lower, "text/markdown") || strings.Contains(lower, "application/xml") ||
		strings.Contains(lower, "text/xml") {
		return false
	}
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "<!doctype html") || strings.HasPrefix(strings.ToLower(trimmed), "<html")
}

// htmlToText converts an HTML document to readable plain text and extracts its
// title. It deliberately avoids a DOM dependency: scripts/styles are dropped,
// block tags become line breaks and the remaining markup is stripped.
func htmlToText(input string) (title, text string) {
	if m := titleTag.FindStringSubmatch(input); len(m) == 2 {
		title = stripTags(m[1])
	}
	cleaned := scriptStyleBlock.ReplaceAllString(input, " ")
	cleaned = htmlComment.ReplaceAllString(cleaned, " ")
	cleaned = blockTag.ReplaceAllString(cleaned, "\n")
	cleaned = anyTag.ReplaceAllString(cleaned, "")
	cleaned = html.UnescapeString(cleaned)
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	lines := strings.Split(cleaned, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(whitespaceRun.ReplaceAllString(line, " "))
	}
	cleaned = strings.Join(lines, "\n")
	cleaned = blankLineRun.ReplaceAllString(cleaned, "\n\n")
	return title, strings.TrimSpace(cleaned)
}

func stripTags(input string) string {
	stripped := htmlTagAny.ReplaceAllString(input, " ")
	stripped = html.UnescapeString(stripped)
	stripped = strings.TrimSpace(allWhitespace.ReplaceAllString(stripped, " "))
	return stripped
}
