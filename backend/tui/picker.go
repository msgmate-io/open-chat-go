// Package tui holds the core-side data models the terminal UI (the private
// `tui` integration) renders for its `:command`s (ci#1107).
//
// The full-screen TUI itself lives in the private integration, but two of its
// commands need server-side knowledge the core already owns:
//
//   - `:sessions` lists the past coding sessions of the user (the persisted,
//     detached-but-alive sandbox sessions from ci#1060) so the user can scroll
//     through them and attach to one.
//   - `:bots` lists the bots an interaction may be handed to, so the user can
//     switch the bot a chat talks to.
//
// Both commands render a picker, and both pickers must apply the same rules no
// matter which client draws them: hide dead/expired rows, collapse duplicates,
// keep the current selection visible and stay deterministic. That logic lives
// here so it is unit-tested in the core instead of being re-implemented (and
// subtly diverging) in each client.
package tui

import (
	"sort"
	"strings"
	"time"

	"backend/sandboxsession"
)

// SessionEntry is one row of the `:sessions` picker.
//
// The attach token is deliberately not part of the entry: the picker only
// needs an identity to select, and the client resolves a fresh, short-lived
// attach token for the chosen session (ci#1060) instead of rendering a
// credential into the terminal scrollback.
type SessionEntry struct {
	// SessionUUID identifies the persisted session record.
	SessionUUID string `json:"session_uuid"`
	// ChatUUID is the interaction the session belongs to (may be empty for a
	// session that was never bound to a chat).
	ChatUUID string `json:"chat_uuid,omitempty"`
	// InteractionUUID is the interaction record the session was opened for.
	InteractionUUID string `json:"interaction_uuid,omitempty"`
	// Provider names the sandbox provider that owns the session.
	Provider string `json:"provider,omitempty"`
	// Label is the single-line text the picker shows for the row.
	Label string `json:"label"`
	// Detached marks a session whose shell was detached but whose sandbox is
	// still alive, i.e. the rows `:sessions` exists for.
	Detached bool `json:"detached"`
	// Current marks the session the TUI is already attached to.
	Current bool `json:"current"`
	// CreatedAt is the session creation time (the picker's sort key).
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is when the session stops being re-attachable.
	ExpiresAt time.Time `json:"expires_at"`
}

// BuildSessionPicker projects persisted sessions into `:sessions` rows.
//
// A row is dropped when the session was destroyed, is past its expiry at now,
// or carries no session uuid. Rows are collapsed per chat (an interaction
// accumulates a new record per re-attach; the picker offers the chat once,
// pointing at its newest session) and sorted newest first with the uuid as a
// stable tie-break, so two clients render the same order.
//
// The returned slice is never nil, so a client can range over it directly.
func BuildSessionPicker(records []sandboxsession.SessionRecord, currentSessionUUID string, now time.Time) []SessionEntry {
	current := strings.TrimSpace(currentSessionUUID)
	entries := make([]SessionEntry, 0, len(records))
	seenChat := make(map[string]bool, len(records))
	seenSession := make(map[string]bool, len(records))

	// Newest first so the per-chat collapse keeps the most recent record.
	sorted := make([]sandboxsession.SessionRecord, 0, len(records))
	for _, record := range records {
		sorted = append(sorted, record)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].UUID < sorted[j].UUID
	})

	for _, record := range sorted {
		uuid := strings.TrimSpace(record.UUID)
		if uuid == "" || record.Destroyed() || record.Expired(now) {
			continue
		}
		if seenSession[uuid] {
			continue
		}
		seenSession[uuid] = true
		chatUUID := strings.TrimSpace(record.ChatUUID)
		if chatUUID != "" {
			if seenChat[chatUUID] {
				continue
			}
			seenChat[chatUUID] = true
		}
		entries = append(entries, SessionEntry{
			SessionUUID:     uuid,
			ChatUUID:        chatUUID,
			InteractionUUID: strings.TrimSpace(record.InteractionUUID),
			Provider:        strings.TrimSpace(record.Provider),
			Label:           sessionLabel(record),
			Detached:        record.Detached(),
			Current:         current != "" && uuid == current,
			CreatedAt:       record.CreatedAt,
			ExpiresAt:       record.ExpiresAt,
		})
	}
	return entries
}

// sessionLabel builds the one-line row label: the most specific identifier the
// record has, so a picker row is still meaningful without the chat title the
// client resolves separately.
func sessionLabel(record sandboxsession.SessionRecord) string {
	if chat := strings.TrimSpace(record.ChatUUID); chat != "" {
		return chat
	}
	if interaction := strings.TrimSpace(record.InteractionUUID); interaction != "" {
		return interaction
	}
	return strings.TrimSpace(record.UUID)
}

// BotEntry is one row of the `:bots` picker.
type BotEntry struct {
	// UUID is the bot runtime uuid the switch request targets.
	UUID string `json:"uuid"`
	// Username is the bot user's handle, the value `--bot` accepts.
	Username string `json:"username,omitempty"`
	// Name is the human-readable bot name.
	Name string `json:"name,omitempty"`
	// IntegrationName is the integration that declared the bot (empty for a
	// user-created bot).
	IntegrationName string `json:"integration_name,omitempty"`
	// Label is the single-line text the picker shows for the row.
	Label string `json:"label"`
	// Current marks the bot the interaction is currently talking to.
	Current bool `json:"current"`
}

// BotCandidate is the input shape for BuildBotPicker: the subset of a listed
// bot a picker needs.
type BotCandidate struct {
	UUID            string
	Username        string
	Name            string
	IntegrationName string
}

// BuildBotPicker projects listed bots into `:bots` rows.
//
// Rows without a uuid are dropped (nothing to switch to), duplicates are
// collapsed by uuid, and the current bot is always the first row so a
// scrolled or truncated picker cannot hide the bot the interaction is on. The
// remaining rows are sorted by label and then uuid, which keeps the order
// stable across clients and reloads.
func BuildBotPicker(candidates []BotCandidate, currentBotUUID string) []BotEntry {
	current := strings.TrimSpace(currentBotUUID)
	entries := make([]BotEntry, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))

	for _, candidate := range candidates {
		uuid := strings.TrimSpace(candidate.UUID)
		if uuid == "" || seen[uuid] {
			continue
		}
		seen[uuid] = true
		name := strings.TrimSpace(candidate.Name)
		username := strings.TrimSpace(candidate.Username)
		label := name
		if label == "" {
			label = username
		}
		if label == "" {
			label = uuid
		}
		entries = append(entries, BotEntry{
			UUID:            uuid,
			Username:        username,
			Name:            name,
			IntegrationName: strings.TrimSpace(candidate.IntegrationName),
			Label:           label,
			Current:         current != "" && uuid == current,
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
		return entries[i].UUID < entries[j].UUID
	})
	return entries
}
