package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ListedChat struct {
	UUID          string            `json:"uuid"`
	Partner       database.User     `json:"partner"`
	LatestMessage *database.Message `json:"latest_message"`
	ChatType      string            `json:"chat_type"`
	Config        interface{}       `json:"config"`
	Settings      *ChatSettingsView `json:"settings,omitempty"`
	Tags          []string          `json:"tags"`
	// LatestMessageAt is the timestamp of the chat's most recent message (or the
	// chat's own update time when it has no messages yet). The UI uses it for
	// the fine-grained time dividers and the time-range filter.
	LatestMessageAt *time.Time `json:"latest_message_at,omitempty"`
	ChatShareUUID   string     `json:"chat_share_uuid,omitempty"`
	SharedChatURL   string     `json:"shared_interaction_url,omitempty"`
	// AdminView is set when an admin is listing/viewing a chat they do not
	// participate in, so the UI can clearly mark that another user's chat is
	// being rendered.
	AdminView *AdminChatView `json:"admin_view,omitempty"`
}

// AdminChatView identifies the true participants of a chat an admin is
// inspecting without being one of them.
type AdminChatView struct {
	User1 database.User `json:"user1"`
	User2 database.User `json:"user2"`
}

type ListedChatsPage struct {
	Limit      int          `json:"limit"`
	Page       int          `json:"page"`
	TotalPages int          `json:"total_pages"`
	Rows       []ListedChat `json:"rows"`
}

// parseChatTimeParam parses an optional RFC3339 timestamp query parameter. An
// empty value returns (nil, nil) so callers can treat it as "unset".
func parseChatTimeParam(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func convertChatToListedChat(user *database.User, chat database.Chat) ListedChat {
	var partner database.User
	if chat.User1Id == user.ID {
		partner = chat.User2
	} else {
		partner = chat.User1
	}

	var config interface{}
	if chat.SharedConfig != nil {
		// The ConfigData is already JSON, just unmarshal it
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err != nil {
			log.Printf("Error unmarshaling config data: %v", err)
		}
	}

	tags := []string(chat.Tags)
	if tags == nil {
		tags = []string{}
	}

	var latestAt *time.Time
	if chat.LatestMessage != nil {
		t := chat.LatestMessage.CreatedAt
		latestAt = &t
	} else if !chat.UpdatedAt.IsZero() {
		t := chat.UpdatedAt
		latestAt = &t
	}

	listed := ListedChat{
		UUID:            chat.UUID,
		Partner:         partner,
		ChatType:        chat.ChatType,
		LatestMessage:   chat.LatestMessage,
		Config:          config,
		Tags:            tags,
		LatestMessageAt: latestAt,
	}
	if adminIsExternalViewer(user, chat) {
		listed.AdminView = &AdminChatView{User1: chat.User1, User2: chat.User2}
	}
	return listed
}

// List returns a list of chats for a specified user.
//
//	@Summary      Get user chats
//	@Description  Retrieve a list of chats for the authenticated user
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        page  query  int  false  "Page number"  default(1)
//	@Param        limit query  int  false  "Page size"     default(40)
//	@Param        chat_types query string false "Chat types to filter by"
//	@Param        q query string false "Free-text search over chat titles"
//	@Param        tags query string false "Comma-separated tags (AND semantics)"
//	@Param        exclude_tags query string false "Comma-separated tags to exclude (a chat carrying any is hidden)"
//	@Param        time_from query string false "Only chats with activity at/after this RFC3339 timestamp"
//	@Param        time_to query string false "Only chats with activity at/before this RFC3339 timestamp"
//	@Success      200 {object} chats.ListedChatsPage "Paginated list of chats"
//	@Failure      400 {string} string "Unable to get database or user"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/chats/list [get]
func (h *ChatsHandler) List(w http.ResponseWriter, r *http.Request) {
	var chats []database.Chat

	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 40}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	// Build the base query. Admins may request scope=all to inspect every chat
	// ("see all" mode); everyone else only sees their own chats.
	query := DB.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID)
	if user.IsAdmin && strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "all") {
		query = DB
	}

	// Restricted browser tokens may only see interaction chats, regardless of the
	// requested chat_types filter.
	if database.IsBrowserToken(r.Context()) {
		if chatTypesParam := r.URL.Query().Get("chat_types"); chatTypesParam != "" {
			for _, chatType := range strings.Split(chatTypesParam, ",") {
				if strings.TrimSpace(chatType) != "interaction" {
					http.Error(w, "Browser tokens can only list interaction chats", http.StatusForbidden)
					return
				}
			}
		}
		query = query.Where("chat_type = ? OR chat_type LIKE ?", "interaction", "interaction:%")
	} else if chatTypesParam := r.URL.Query().Get("chat_types"); chatTypesParam != "" {
		// Handle chat_types filter
		chatTypes := strings.Split(chatTypesParam, ",")
		if len(chatTypes) > 0 {

			// Check if we need wildcard matching for namespaced chat types
			hasIntegration := false
			hasInteraction := false
			for _, chatType := range chatTypes {
				if chatType == "integration" {
					hasIntegration = true
				}
				if chatType == "interaction" {
					hasInteraction = true
				}
			}

			// Build the query conditions
			if hasIntegration && hasInteraction {
				query = query.Where(
					"chat_type IN ? OR chat_type LIKE ? OR chat_type LIKE ?",
					chatTypes,
					"integration:%",
					"interaction:%",
				)
			} else if hasIntegration {
				query = query.Where("chat_type IN ? OR chat_type LIKE ?", chatTypes, "integration:%")
			} else if hasInteraction {
				query = query.Where("chat_type IN ? OR chat_type LIKE ?", chatTypes, "interaction:%")
			} else {
				// Use the original exact matching
				query = query.Where("chat_type IN ?", chatTypes)
			}
		}
	}

	// Filter by tag/category. Multiple tags use AND semantics: a chat must
	// carry every requested tag. Tags are stored as a JSON array, so a quoted
	// substring match on the serialized value matches whole tag names only.
	if tagsParam := strings.TrimSpace(r.URL.Query().Get("tags")); tagsParam != "" {
		for _, tag := range strings.Split(tagsParam, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			query = query.Where("CAST(tags AS TEXT) LIKE ?", "%\""+tag+"\"%")
		}
	}

	// Exclude by tag/category. A chat carrying any excluded tag is hidden, so
	// `exclude_tags=automation,trigger` drops automation- and trigger-generated
	// chats. COALESCE keeps chats with no tags (NULL) in the result.
	if excludeParam := strings.TrimSpace(r.URL.Query().Get("exclude_tags")); excludeParam != "" {
		for _, tag := range strings.Split(excludeParam, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			query = query.Where("COALESCE(CAST(tags AS TEXT), '') NOT LIKE ?", "%\""+tag+"\"%")
		}
	}

	// Free-text search over chat titles. Titles live in the chat settings JSON
	// blob; CAST(... AS TEXT) keeps this portable across SQLite and Postgres.
	if search := strings.TrimSpace(r.URL.Query().Get("q")); search != "" {
		titleSubquery := DB.Model(&database.ChatSettings{}).
			Select("chat_id").
			Where("CAST(config_data AS TEXT) LIKE ?", "%"+search+"%")
		query = query.Where("id IN (?)", titleSubquery)
	}

	// Optional inclusive time range over the chat's latest activity.
	timeFrom, fromErr := parseChatTimeParam(r.URL.Query().Get("time_from"))
	if fromErr != nil {
		http.Error(w, "Invalid time_from (expected RFC3339)", http.StatusBadRequest)
		return
	}
	timeTo, toErr := parseChatTimeParam(r.URL.Query().Get("time_to"))
	if toErr != nil {
		http.Error(w, "Invalid time_to (expected RFC3339)", http.StatusBadRequest)
		return
	}
	if timeFrom != nil {
		query = query.Where("latest_message_id IN (?)", DB.Model(&database.Message{}).Select("id").Where("created_at >= ?", *timeFrom))
	}
	if timeTo != nil {
		query = query.Where("latest_message_id IN (?)", DB.Model(&database.Message{}).Select("id").Where("created_at <= ?", *timeTo))
	}

	// Apply pagination and preloads. The count must run against the filtered
	// query so TotalPages reflects the active filters.
	q := query.Scopes(database.Paginate(&chats, &pagination, query)).
		Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Preload("LatestMessage").
		Find(&chats)

	if q.Error != nil {
		// A single corrupt message row must never blank the whole chat list.
		// Retry without the latest-message preload so the list still renders.
		log.Printf("Failed to list chats with latest message preload, retrying without it: %v", q.Error)
		chats = nil
		q = query.Scopes(database.Paginate(&chats, &pagination, query)).
			Preload("User1").
			Preload("User2").
			Preload("SharedConfig").
			Find(&chats)
		if q.Error != nil {
			http.Error(w, q.Error.Error(), http.StatusInternalServerError)
			return
		}
	}

	listedChats := make([]ListedChat, len(chats))
	for i, chat := range chats {
		listedChats[i] = convertChatToListedChat(user, chat)
	}

	chatIDs := make([]uint, 0, len(chats))
	for _, chat := range chats {
		chatIDs = append(chatIDs, chat.ID)
	}
	settingsByChat := loadChatSettingsViews(DB, chatIDs)
	for i := range listedChats {
		if view := settingsByChat[chats[i].ID]; view != nil {
			listedChats[i].Settings = view
		}
	}

	response := ListedChatsPage{
		Limit:      pagination.Limit,
		Page:       pagination.Page,
		TotalPages: pagination.TotalPages,
		Rows:       listedChats,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
