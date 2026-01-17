// Package webtools provides shared, agent-facing web helpers: a readable
// fetch (HTML-to-text with a bounded size) and a keyless web search. It backs
// the core web_fetch/web_search built-in tools as well as the msgmate coder's
// coder_webfetch/coder_websearch tools, so every agent uses one implementation.
package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultMaxBytes bounds how much of a fetched document is decoded.
	DefaultMaxBytes = 512 * 1024
	// DefaultSearchLimit is the number of search results returned by default.
	DefaultSearchLimit = 8
	// MaxSearchLimit caps the requested number of search results.
	MaxSearchLimit = 20

	// networkAttempts is how many times a transient network failure (a
	// connection reset, EOF or timeout) is retried before giving up. The
	// keyless DuckDuckGo fallback intermittently resets connections from
	// datacenter IPs, so a single attempt surfaces as a spurious tool error.
	networkAttempts = 3
	// networkRetryBaseDelay is the backoff before the first retry; it grows
	// linearly per attempt.
	networkRetryBaseDelay = 300 * time.Millisecond

	fetchUserAgent = "Mozilla/5.0 (compatible; OpenChatBot/1.0; +https://msgmate.io)"
)

// httpClient is shared across web tool requests so connections are pooled and
// reused, which reduces the chance of a transient reset.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// ErrSearchUnavailable signals that a search backend answered with a 2xx
// response that is not a usable result set: an anti-bot/challenge page or an
// empty response with no result markup. It lets callers (and the model)
// distinguish "search is down" from a genuine "no matches", instead of
// silently surfacing count:0.
var ErrSearchUnavailable = errors.New("search backend unavailable")

// challengeMarkers are fragments found in anti-bot/challenge pages served in
// place of search results. DuckDuckGo answers its keyless HTML endpoint with
// HTTP 202 and an "anomaly" page when it decides the caller looks like a bot;
// other deployments reuse the same markers. Matching is case-insensitive.
var challengeMarkers = []string{
	"anomaly",
	"duckduckgo.com/challenge",
	"duckduckgo.com/anomaly",
	"bots use duckduckgo",
	"unusual traffic",
	"captcha",
}

// ddgNoResultsMarkers identify a genuine empty-result page from the DuckDuckGo
// HTML endpoint, so an intentional "no matches" is not misreported as a broken
// backend.
var ddgNoResultsMarkers = []string{
	"no-results",
	"no results",
}

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

// doRequest executes an HTTP request, retrying transient network failures (a
// connection reset, EOF or timeout) up to networkAttempts times with a short
// linear backoff. build must return a fresh request for every attempt because
// a request body can only be read once. The caller's context bounds the total
// time spent, so retries stop as soon as it is done.
func doRequest(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= networkAttempts; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastErr = err
		if !isTransientNetworkError(err) || attempt == networkAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * networkRetryBaseDelay):
		}
	}
	return nil, lastErr
}

// isTransientNetworkError reports whether err is a transient network failure
// worth retrying: connection resets/refusals, broken pipes, EOFs and timeouts.
// Permanent failures (a not-found DNS name, context cancellation or a deadline
// already reached) are not retried.
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false
	}
	// A net.Error covers timeouts, but *url.Error also implements net.Error
	// for every transport failure, so only an actual timeout qualifies here;
	// resets and refusals are caught by the syscall checks below.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"connection reset by peer",
		"connection refused",
		"broken pipe",
		"i/o timeout",
		"tls handshake timeout",
		"unexpected eof",
		"server closed idle connection",
	} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

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

	resp, err := doRequest(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", fetchUserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.5")
		return req, nil
	})
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
// and otherwise falls back to the keyless DuckDuckGo HTML endpoint. A backend
// that answers with a 2xx but no usable results (an anti-bot/challenge page or
// an empty payload) fails with ErrSearchUnavailable rather than an empty set.
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
	resp, err := doRequest(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", fetchUserAgent)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("search endpoint returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, DefaultMaxBytes))
	if err != nil {
		return nil, err
	}
	if isChallengeResponse(resp.StatusCode, body) {
		return nil, fmt.Errorf("%w: search endpoint returned an anti-bot challenge page (status %d)", ErrSearchUnavailable, resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: invalid search response (status %d): %v", ErrSearchUnavailable, resp.StatusCode, err)
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
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: search endpoint returned no results (status %d)", ErrSearchUnavailable, resp.StatusCode)
	}
	return out, nil
}

// isChallengeResponse reports whether a 2xx response is an anti-bot/challenge
// page rather than a usable result payload. DuckDuckGo uses HTTP 202 for its
// "anomaly" page; some deployments return 200 with the same body markers.
func isChallengeResponse(status int, body []byte) bool {
	if status == http.StatusAccepted {
		return true
	}
	if status < 200 || status > 299 {
		return false
	}
	return containsAnyFold(string(body), challengeMarkers)
}

// containsAnyFold reports whether body contains any of markers,
// case-insensitively.
func containsAnyFold(body string, markers []string) bool {
	lower := strings.ToLower(body)
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// searchDuckDuckGo parses the keyless DuckDuckGo HTML endpoint. It is a
// best-effort fallback used when no search backend is configured. Transient
// connection resets (common from datacenter IPs) are retried by doRequest.
func searchDuckDuckGo(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("q", query)
	form := params.Encode()
	resp, err := doRequest(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://html.duckduckgo.com/html/", strings.NewReader(form))
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", fetchUserAgent)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %w", err)
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
	if isChallengeResponse(resp.StatusCode, body) {
		return nil, fmt.Errorf("%w: duckduckgo returned an anti-bot challenge page (status %d)", ErrSearchUnavailable, resp.StatusCode)
	}

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
	// A usable response that parses to zero results is an anomaly unless
	// DuckDuckGo explicitly said it found nothing; otherwise the challenge
	// page's 2xx would keep yielding a silent count:0.
	if len(out) == 0 && !containsAnyFold(page, ddgNoResultsMarkers) {
		return nil, fmt.Errorf("%w: duckduckgo returned no parseable results (status %d)", ErrSearchUnavailable, resp.StatusCode)
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
