package tui

import (
	"sort"
	"strings"
)

// This file defines the read-only catalog overlays of the `:command` palette
// (ci#1107): `:models` renders the model catalog served by `GET /models` and
// `:tools` renders the tool catalog served by `GET /tools`. `:bots` keeps
// switching the bot of the current interaction, its rows come from
// BuildBotPicker over `GET /bots/list`.
//
// Both catalog overlays are strictly read-only: nothing here writes a server
// setting, changes the model of an interaction or invokes a tool. The
// projections live in the core so that every integration renders the same rows
// in the same order, exactly like BuildSessionPicker and BuildBotPicker do for
// the pickable commands.

// catalogRoutes maps a palette action to the read-only catalog route it reads.
//
// Keeping the routes next to the actions means the client never has to hardcode
// (and drift from) the `coder-tui` token allowlist.
var catalogRoutes = map[Action]string{
	ActionPickBot:   "/bots/list",
	ActionPickModel: "/models",
	ActionPickTool:  "/tools",
}

// CatalogRoute returns the HTTP route a catalog palette action reads. It reports
// false for actions that are not backed by a catalog route, for example
// `:sessions`, which reads the local session store.
func CatalogRoute(action Action) (string, bool) {
	route, ok := catalogRoutes[action]
	return route, ok
}

// ModelEntry is one row of the `:models` overlay.
type ModelEntry struct {
	// ID is the model identifier the API accepts, for example
	// "gpt-4o-mini-2024-07-18".
	ID string `json:"id"`
	// Name is the display name of the model, empty when the catalog only
	// carries an id.
	Name string `json:"name,omitempty"`
	// Provider is the backend that serves the model ("openai", "anthropic", ...).
	Provider string `json:"provider,omitempty"`
	// Description is the optional one-line model description.
	Description string `json:"description,omitempty"`
	// ContextWindow is the advertised context size in tokens, zero when the
	// catalog does not advertise one.
	ContextWindow int `json:"context_window,omitempty"`
	// Label is the single-line text the overlay shows for the row.
	Label string `json:"label"`
	// Current marks the model the interaction would use right now.
	Current bool `json:"current"`
}

// ModelCandidate is the input shape for BuildModelPicker: the subset of a
// listed model the overlay needs.
type ModelCandidate struct {
	ID            string
	Name          string
	Provider      string
	Description   string
	ContextWindow int
}

// BuildModelPicker projects a listed model catalog into `:models` rows.
//
// Rows without an id are dropped (nothing to select), duplicates are collapsed
// by id, and the current model is always the first row so a scrolled or
// truncated overlay cannot hide the model the interaction is on. The remaining
// rows are sorted by label and then id, which keeps the order stable across
// clients and reloads.
func BuildModelPicker(candidates []ModelCandidate, currentModelID string) []ModelEntry {
	current := strings.TrimSpace(currentModelID)
	entries := make([]ModelEntry, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))

	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(candidate.Name)
		label := name
		if label == "" {
			label = id
		}
		entries = append(entries, ModelEntry{
			ID:            id,
			Name:          name,
			Provider:      strings.TrimSpace(candidate.Provider),
			Description:   strings.TrimSpace(candidate.Description),
			ContextWindow: candidate.ContextWindow,
			Label:         label,
			Current:       current != "" && id == current,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Current != entries[j].Current {
			return entries[i].Current
		}
		left, right := strings.ToLower(entries[i].Label), strings.ToLower(entries[j].Label)
		if left != right {
			return left < right
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

// ToolEntry is one row of the `:tools` overlay.
type ToolEntry struct {
	// Name is the tool name the API accepts.
	Name string `json:"name"`
	// Description is the one-line tool description.
	Description string `json:"description,omitempty"`
	// Category groups tools the way the catalog does, empty when the catalog
	// does not group them.
	Category string `json:"category,omitempty"`
	// Label is the single-line text the overlay shows for the row.
	Label string `json:"label"`
}

// ToolCandidate is the input shape for BuildToolPicker: the subset of a listed
// tool the overlay needs.
type ToolCandidate struct {
	Name        string
	Description string
	Category    string
}

// BuildToolPicker projects a listed tool catalog into `:tools` rows.
//
// The overlay only shows what the agent can call, it never calls a tool, so the
// rows carry no selection state. Rows without a name are dropped, duplicates are
// collapsed by name and the rest is sorted by label and then name.
func BuildToolPicker(candidates []ToolCandidate) []ToolEntry {
	entries := make([]ToolEntry, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))

	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, ToolEntry{
			Name:        name,
			Description: strings.TrimSpace(candidate.Description),
			Category:    strings.TrimSpace(candidate.Category),
			Label:       name,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		left, right := strings.ToLower(entries[i].Label), strings.ToLower(entries[j].Label)
		if left != right {
			return left < right
		}
		return entries[i].Name < entries[j].Name
	})
	return entries
}
