package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"backend/webtools"
)

// WebFetchInput is the input for the web_fetch tool.
type WebFetchInput struct {
	URL      string `json:"url"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// WebSearchInput is the input for the web_search tool.
type WebSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Site  string `json:"site,omitempty"`
}

// WebFetchToolDef fetches a URL and returns readable text/code plus metadata.
// It is the shared fetch tool available to any bot that lists `web_fetch`.
var WebFetchToolDef = ToolDefinition{
	Name:           "web_fetch",
	Description:    "Fetch a URL and return its readable text content (HTML is converted to plain text). Use it to read web pages, documentation or API responses.",
	InputType:      WebFetchInput{},
	RequiredParams: []string{"url"},
	Parameters: map[string]interface{}{
		"url":       map[string]interface{}{"type": "string", "description": "Absolute URL (https:// is assumed when the scheme is omitted)."},
		"max_bytes": map[string]interface{}{"type": "integer", "description": "Optional cap on the decoded body size (default 524288)."},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		in, ok := input.(WebFetchInput)
		if !ok {
			return "", fmt.Errorf("invalid input")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		result, err := webtools.Fetch(ctx, in.URL, in.MaxBytes)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	},
}

// WebSearchToolDef searches the web and returns the top results. It is the
// shared search tool available to any bot that lists `web_search`; a
// SearXNG-compatible backend can be configured with OPENCHAT_WEB_SEARCH_URL.
var WebSearchToolDef = ToolDefinition{
	Name:           "web_search",
	Description:    "Search the web for documentation, an error message or a topic and return the top results (title, URL, snippet). Follow up with web_fetch to read a result.",
	InputType:      WebSearchInput{},
	RequiredParams: []string{"query"},
	Parameters: map[string]interface{}{
		"query": map[string]interface{}{"type": "string", "description": "The search query."},
		"limit": map[string]interface{}{"type": "integer", "description": "Maximum results to return (default 8, max 20)."},
		"site":  map[string]interface{}{"type": "string", "description": "Optional site restriction, e.g. \"docs.rs\"."},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		in, ok := input.(WebSearchInput)
		if !ok {
			return "", fmt.Errorf("invalid input")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		results, err := webtools.Search(ctx, in.Query, in.Limit, in.Site)
		if err != nil {
			return "", err
		}
		if results == nil {
			results = []webtools.SearchResult{}
		}
		encoded, err := json.Marshal(map[string]interface{}{
			"query":   strings.TrimSpace(in.Query),
			"results": results,
			"count":   len(results),
		})
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	},
}
