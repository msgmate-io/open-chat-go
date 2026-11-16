package tools

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// This file implements the `openchat_self_history` MCP tool (ci#1103): an
// additive, read-only tool that lets an agent page back over ITS OWN past
// interactions for the current acting user.
//
// Authorisation is the whole point of the tool, so it is derived from the
// credential only - never from an argument:
//
//	self       := the automated user bound to the MCP credential (the caller)
//	actingUser := the human the agent acts for (bot runtime owner, else the caller)
//
// A row is returned only when the chat is an interaction AND either
//   - the (self, actingUser) participant pair matches, or
//   - the chat was explicitly shared with actingUser (SharedChatInstance.TargetUserId).
//
// There is deliberately no `agent` argument (unlike openchat_list_interactions,
// whose applyInteractionFilters trusts one), no admin widening, and no
// participation-or-scope-tag widening: a second agent of the same owner and a
// third user's interaction stay invisible.
//
// Summaries are derived on read (no persisted summary column, no migration).

// selfHistoryToolDefinition returns the tool descriptor for
// `openchat_self_history`. The schema intentionally omits `agent`.
func selfHistoryToolDefinition() mcpToolDefinition {
	return mcpToolDefinition{
		Name: "openchat_self_history",
		Description: "Look back at YOUR OWN past interactions as this agent: page back over the interaction chats " +
			"this agent ran for its acting user (chat uuid, tags, state, derived summary, last agent text, ended-at). " +
			"Omit chat_uuid to list history; pass chat_uuid to get that one interaction plus its recent transcript. " +
			"The identity is always derived from the credential - there is no `agent` argument and admins are not widened.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"chat_uuid": map[string]interface{}{
					"type":        "string",
					"description": "Optional: return this one interaction plus its recent transcript (still authorisation-checked).",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Optional free text matched against title, label, tags and message text.",
				},
				"repo": map[string]interface{}{
					"type":        "string",
					"description": "Optional: filter by workspace repository (matches the repo:<name> tag).",
				},
				"project": map[string]interface{}{
					"type":        "string",
					"description": "Alias of repo.",
				},
				"tag": map[string]interface{}{
					"type":        "string",
					"description": "Optional: filter by an exact chat tag.",
				},
				"since": map[string]interface{}{
					"type":        "string",
					"description": "Optional RFC3339 lower bound on the interaction's last update.",
				},
				"until": map[string]interface{}{
					"type":        "string",
					"description": "Optional RFC3339 upper bound on the interaction's last update.",
				},
				"state": map[string]interface{}{
					"type":        "string",
					"description": "Optional state filter: running, finished, waiting.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum rows to return (default %d, max %d).", mcpDefaultLimit, mcpMaxLimit),
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "Rows to skip for pagination.",
				},
			},
			"required": []string{},
		},
	}
}

// selfHistoryActors resolves the (self, actingUser) pair from the credential.
// It mirrors resolveActingUser (backend/api/bots/Bots.go) for the owner lookup
// without importing that package, so the tools package keeps no dependency on
// the bots handlers.
func selfHistoryActors(db *gorm.DB, user *database.User) (*database.User, *database.User, error) {
	if user == nil {
		return nil, nil, errors.New("no authenticated user")
	}
	if !user.IsAutomated {
		return nil, nil, errors.New("openchat_self_history is only available to an agent credential: the caller is not an automated agent")
	}
	self := user
	acting := user
	var runtime database.BotRuntimeConfig
	if err := db.Preload("OwnerUser").
		Where("bot_user_id = ? AND is_active = ?", user.ID, true).
		First(&runtime).Error; err == nil && runtime.OwnerUser.ID != 0 {
		owner := runtime.OwnerUser
		acting = &owner
	}
	if self.ID == acting.ID {
		return nil, nil, errors.New("openchat_self_history could not resolve a distinct acting user for this agent credential")
	}
	return self, acting, nil
}

// selfHistoryQuery applies the authorisation predicate plus the optional
// repo/tag/time filters. Nothing here can widen visibility.
func selfHistoryQuery(db *gorm.DB, self *database.User, acting *database.User, args map[string]interface{}) *gorm.DB {
	pair := "(user1_id = ? AND user2_id = ?) OR (user2_id = ? AND user1_id = ?)"
	shared := "id IN (SELECT chat_id FROM shared_chat_instances WHERE target_user_id = ?)"
	query := db.Model(&database.Chat{}).
		Where("chat_type LIKE ?", "interaction%").
		Where("("+pair+") OR ("+shared+" AND (user1_id = ? OR user2_id = ?))",
			self.ID, acting.ID, self.ID, acting.ID,
			acting.ID,
			self.ID, self.ID,
		)

	repo := mcpArgString(args, "repo")
	if repo == "" {
		repo = mcpArgString(args, "project")
	}
	if repo != "" {
		query = query.Where("CAST(tags AS TEXT) LIKE ?", "%repo:"+repo+"%")
	}
	if tag := mcpArgString(args, "tag"); tag != "" {
		query = query.Where("CAST(tags AS TEXT) LIKE ?", "%"+tag+"%")
	}
	if since := parseSelfHistoryTime(mcpArgString(args, "since")); since != nil {
		query = query.Where("updated_at >= ?", *since)
	}
	if until := parseSelfHistoryTime(mcpArgString(args, "until")); until != nil {
		query = query.Where("updated_at <= ?", *until)
	}
	if search := mcpArgString(args, "query"); search != "" {
		like := "%" + search + "%"
		titleSubquery := db.Model(&database.ChatSettings{}).
			Select("chat_id").
			Where("CAST(config_data AS TEXT) LIKE ?", like)
		labelSubquery := db.Table("automation_chat_labels").
			Select("chat_uuid").
			Where("label LIKE ?", like)
		query = query.Where("id IN (?) OR uuid IN (?) OR CAST(tags AS TEXT) LIKE ?", titleSubquery, labelSubquery, like)
	}
	return query
}

func parseSelfHistoryTime(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return &parsed
}

// loadSelfInteraction loads one interaction through the same self predicate, so
// a chat_uuid argument can never reach a chat the agent may not see.
func loadSelfInteraction(db *gorm.DB, self *database.User, acting *database.User, chatUUID string) (database.Chat, error) {
	chat := database.Chat{}
	err := selfHistoryQuery(db, self, acting, map[string]interface{}{}).
		Where("uuid = ?", chatUUID).
		Preload("User1").
		Preload("User2").
		Preload("LatestMessage").
		Preload("LatestMessage.Sender").
		First(&chat).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return chat, fmt.Errorf("interaction %s not found or not accessible to this agent", chatUUID)
		}
		return chat, fmt.Errorf("failed to load interaction: %w", err)
	}
	return chat, nil
}

// selfHistoryTextColumn reads message text as text, so the tool never depends on
// the message payload column type.
func selfHistoryText(db *gorm.DB, chatID uint, automated bool, ascending bool) string {
	order := "desc"
	if ascending {
		order = "asc"
	}
	var text string
	_ = db.Raw(
		"SELECT CAST(m.text AS TEXT) FROM messages m JOIN users u ON u.id = m.sender_id "+
			"WHERE m.chat_id = ? AND u.is_automated = ? AND m.text IS NOT NULL AND CAST(m.text AS TEXT) <> '' "+
			"ORDER BY m.created_at "+order+", m.id "+order+" LIMIT 1",
		chatID, automated,
	).Scan(&text).Error
	return strings.TrimSpace(text)
}

type selfHistoryTranscriptRow struct {
	ID             uint      `json:"-"`
	ChatID         uint      `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	Text           string    `json:"text"`
	SenderName     string    `json:"sender"`
	SenderUsername string    `json:"sender_username,omitempty"`
	IsAutomated    bool      `json:"is_automated"`
}

// selfHistoryTranscript returns the most recent messages of one interaction,
// oldest first, so an agent can read what it actually did.
func selfHistoryTranscript(db *gorm.DB, chatID uint, limit int) []selfHistoryTranscriptRow {
	rows := []selfHistoryTranscriptRow{}
	_ = db.Raw(
		"SELECT m.id AS id, m.chat_id AS chat_id, m.created_at AS created_at, CAST(m.text AS TEXT) AS text, "+
			"COALESCE(u.name, '') AS sender_name, COALESCE(u.username, '') AS sender_username, "+
			"COALESCE(u.is_automated, false) AS is_automated "+
			"FROM messages m LEFT JOIN users u ON u.id = m.sender_id "+
			"WHERE m.chat_id = ? AND m.text IS NOT NULL AND CAST(m.text AS TEXT) <> '' "+
			"ORDER BY m.created_at desc, m.id desc LIMIT ?",
		chatID, limit,
	).Scan(&rows).Error
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	for i := range rows {
		rows[i].SenderName = strings.TrimSpace(rows[i].SenderName)
		rows[i].Text = truncateMCPText(rows[i].Text, 4000)
	}
	return rows
}

// buildSelfHistoryRow derives the per-interaction summary on read.
func buildSelfHistoryRow(db *gorm.DB, chat database.Chat) map[string]interface{} {
	row := buildInteractionRow(db, nil, chat)
	firstPrompt := truncateMCPText(selfHistoryText(db, chat.ID, false, true), 500)
	lastAgentText := truncateMCPText(selfHistoryText(db, chat.ID, true, false), 500)
	summary := ""
	if firstPrompt != "" {
		summary = "Prompt: " + firstPrompt
	}
	if lastAgentText != "" {
		if summary != "" {
			summary += "\n\n"
		}
		summary += "Last agent reply: " + lastAgentText
	}
	state := interactionState(chat)
	return map[string]interface{}{
		"uuid":            chat.UUID,
		"chat_type":       chat.ChatType,
		"when":            chat.UpdatedAt,
		"ended_at":        chat.UpdatedAt,
		"state":           state,
		"is_active":       state == "running",
		"title":           row.Title,
		"label":           row.Label,
		"tags":            row.Tags,
		"summary":         summary,
		"first_prompt":    firstPrompt,
		"last_agent_text": lastAgentText,
	}
}

func toolSelfHistory(db *gorm.DB, user *database.User, args map[string]interface{}) (interface{}, error) {
	self, acting, err := selfHistoryActors(db, user)
	if err != nil {
		return nil, err
	}
	identity := map[string]interface{}{
		"agent_uuid":       self.UUID,
		"agent_username":   self.Username,
		"agent_name":       self.Name,
		"acting_user_uuid": acting.UUID,
		"acting_username":  acting.Username,
	}

	chatUUID := mcpArgString(args, "chat_uuid")
	if chatUUID != "" {
		chat, err := loadSelfInteraction(db, self, acting, chatUUID)
		if err != nil {
			return nil, err
		}
		limit := mcpArgInt(args, "limit", mcpDefaultLimit, mcpMaxLimit)
		return map[string]interface{}{
			"self":         identity,
			"interaction":  buildSelfHistoryRow(db, chat),
			"transcript":   selfHistoryTranscript(db, chat.ID, limit),
			"messages_url": "/api/v1/chats/" + chat.UUID + "/messages",
		}, nil
	}

	limit := mcpArgInt(args, "limit", mcpDefaultLimit, mcpMaxLimit)
	offset := mcpArgInt(args, "offset", 0, 0)
	if offset < 0 {
		offset = 0
	}
	stateFilter := strings.TrimSpace(mcpArgString(args, "state"))

	chats := []database.Chat{}
	if err := selfHistoryQuery(db, self, acting, args).
		Preload("User1").
		Preload("User2").
		Preload("LatestMessage").
		Preload("LatestMessage.Sender").
		Order("updated_at desc, id desc").
		Limit(limit).
		Offset(offset).
		Find(&chats).Error; err != nil {
		return nil, fmt.Errorf("failed to load agent self history: %w", err)
	}

	rows := make([]map[string]interface{}, 0, len(chats))
	for _, chat := range chats {
		row := buildSelfHistoryRow(db, chat)
		if stateFilter != "" && row["state"] != stateFilter {
			continue
		}
		rows = append(rows, row)
	}
	return map[string]interface{}{
		"self":         identity,
		"rows":         rows,
		"count":        len(rows),
		"limit":        limit,
		"offset":       offset,
		"history_hint": "Call openchat_self_history with chat_uuid=<uuid> for that interaction's recent transcript.",
	}, nil
}
