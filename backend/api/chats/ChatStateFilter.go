package chats

import (
	"backend/chatstate"
	"backend/database"
	"backend/workqueue"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// ChatStateFilterScanLimit caps how many chats a single state-filtered list
// request resolves. A chat's state cannot be expressed as a SQL predicate on the
// chat row: it depends on live queue activity, pending user confirmations and
// the newest message's metadata. The filter therefore resolves the state of the
// chats matching the other filters in newest-first order and paginates that
// matching subset, which keeps a single request bounded (ci#761).
const ChatStateFilterScanLimit = 1000

// ErrInvalidChatStateFilter is returned for a `state` value that is not one of
// the known interaction states.
var ErrInvalidChatStateFilter = errors.New("invalid chat state filter")

func validChatStateFilterValue(state string) bool {
	switch state {
	case string(chatstate.StateIdle),
		string(chatstate.StateActive),
		string(chatstate.StateFinished),
		string(chatstate.StateFailed),
		string(chatstate.StateNeedsConfirmation):
		return true
	default:
		return false
	}
}

// parseChatStateFilter parses the `state` query parameter, accepting repeated
// parameters and comma-separated values alike. An empty value means "no state
// filter" and returns (nil, nil).
func parseChatStateFilter(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			state := strings.ToLower(strings.TrimSpace(part))
			if state == "" {
				continue
			}
			if !validChatStateFilterValue(state) {
				return nil, fmt.Errorf("%w: %q", ErrInvalidChatStateFilter, state)
			}
			if _, exists := seen[state]; exists {
				continue
			}
			seen[state] = struct{}{}
			out = append(out, state)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ResolveChatStates resolves the state of every given chat with the very same
// resolver the single-chat status endpoint and the batch /api/v1/chats/states
// endpoint use, so the server-side state filter can never disagree with the
// badge the UI polls. A chat whose state cannot be resolved is reported as idle
// instead of failing the whole list.
func ResolveChatStates(DB *gorm.DB, inspector *asynq.Inspector, chats []database.Chat) map[string]string {
	states := make(map[string]string, len(chats))
	// ci#850: take one queue snapshot for the whole batch instead of listing the
	// queue once per chat inside resolveInteractionStatus, which made a list of
	// N chats cost O(N x queue size) Redis scans.
	queue := snapshotQueueActivity{snapshot: workqueue.SnapshotBotReplyActivity(inspector)}
	for i := range chats {
		state := string(chatstate.StateIdle)
		if status, err := resolveInteractionStatusWithQueue(DB, chats[i], queue); err == nil && status.State != "" {
			state = status.State
		}
		states[chats[i].UUID] = state
	}
	return states
}

// ChatStateMatchesFilter reports whether a resolved state passes the filter. An
// empty filter matches everything.
func ChatStateMatchesFilter(state string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, want := range filter {
		if want == state {
			return true
		}
	}
	return false
}

// listChatsFilteredByState serves `GET /api/v1/chats/list?state=...`: it resolves
// the state of the chats matching every other filter, keeps only the rows whose
// state passes the filter and paginates that subset, so `total` / `total_pages`
// describe the filtered set and an old active interaction is reachable on the
// first page without pressing "load more" (ci#761). Pinned chats stay subject to
// the same state filter but are still carried on the first page, exactly like
// the unfiltered pinned top-up (ci#512).
func (h *ChatsHandler) listChatsFilteredByState(
	w http.ResponseWriter,
	DB *gorm.DB,
	user *database.User,
	pagination database.Pagination,
	stateFilter []string,
	newFilteredQuery func() *gorm.DB,
	inspector *asynq.Inspector,
) {
	loadCandidates := func(withLatestMessage bool) (*gorm.DB, []database.Chat) {
		candidates := []database.Chat{}
		q := newFilteredQuery().
			Order("id DESC").
			Limit(ChatStateFilterScanLimit).
			Preload("User1").
			Preload("User2").
			Preload("SharedConfig")
		if withLatestMessage {
			q = q.Preload("LatestMessage")
		}
		return q.Find(&candidates), candidates
	}

	q, candidates := loadCandidates(true)
	if q.Error != nil {
		// A single corrupt message row must never blank the whole list.
		log.Printf("Failed to resolve chats for the state filter with the latest message preload, retrying without it: %v", q.Error)
		q, candidates = loadCandidates(false)
		if q.Error != nil {
			http.Error(w, q.Error.Error(), http.StatusInternalServerError)
			return
		}
	}

	states := ResolveChatStates(DB, inspector, candidates)

	candidateIDs := make([]uint, 0, len(candidates))
	for _, chat := range candidates {
		candidateIDs = append(candidateIDs, chat.ID)
	}
	pinnedByChat := loadChatPins(DB, user.ID, candidateIDs)

	matched := make([]database.Chat, 0, len(candidates))
	pinnedMatched := make([]database.Chat, 0, 8)
	for _, chat := range candidates {
		if !ChatStateMatchesFilter(states[chat.UUID], stateFilter) {
			continue
		}
		matched = append(matched, chat)
		if pinnedByChat[chat.ID] {
			pinnedMatched = append(pinnedMatched, chat)
		}
	}

	limit := pagination.GetLimit()
	pagination.Limit = limit
	total := len(matched)
	pagination.TotalRows = int64(total)
	pagination.TotalPages = int(math.Ceil(float64(total) / float64(limit)))

	offset := pagination.GetOffset()
	window := []database.Chat{}
	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		window = matched[offset:end]
	}

	// A pinned chat that does not match the state filter (pinned + finished
	// while filtering for running chats) stays hidden, but a matching pin must
	// never fall behind the offset window: it is prepended and deduplicated by
	// id, like the ci#512 top-up of the unfiltered list.
	if pagination.Page <= 1 && len(pinnedMatched) > 0 {
		seen := make(map[uint]bool, len(pinnedMatched)+len(window))
		merged := make([]database.Chat, 0, len(pinnedMatched)+len(window))
		for _, chat := range pinnedMatched {
			if !seen[chat.ID] {
				merged = append(merged, chat)
				seen[chat.ID] = true
			}
		}
		for _, chat := range window {
			if !seen[chat.ID] {
				merged = append(merged, chat)
				seen[chat.ID] = true
			}
		}
		window = merged
	}

	listedChats := make([]ListedChat, len(window))
	rowIDs := make([]uint, 0, len(window))
	for i, chat := range window {
		listedChats[i] = convertChatToListedChat(user, chat)
		listedChats[i].State = states[chat.UUID]
		rowIDs = append(rowIDs, chat.ID)
	}
	settingsByChat := loadChatSettingsViews(DB, rowIDs)
	rowPins := loadChatPins(DB, user.ID, rowIDs)
	for i := range listedChats {
		if view := settingsByChat[window[i].ID]; view != nil {
			listedChats[i].Settings = view
		}
		if rowPins[window[i].ID] {
			listedChats[i].Pinned = true
		}
	}

	response := ListedChatsPage{
		Limit:      pagination.Limit,
		Page:       pagination.Page,
		TotalPages: pagination.TotalPages,
		Total:      total,
		Rows:       listedChats,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
