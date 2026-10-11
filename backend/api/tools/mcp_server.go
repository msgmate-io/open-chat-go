package tools

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

// This file implements a small, read-only Open-Chat MCP server ("MCPs from
// inside of open-chat itself"): an HTTP-streamable JSON-RPC 2.0 endpoint that
// exposes the interaction history of the authenticated user as MCP tools.
//
// It exists so a coding agent running through the opencode integration can pull
// context from past Open-Chat interactions (not just from GitHub). The MCP
// server is attached to an opencode project as a remote MCP server (the
// opencode integration renders it into the managed runtime's OpenCode `mcp`
// block); the bearer access token of an admin user scopes it.
//
// Access model: regular users only ever see their own chats; admins (the CI
// admin bots run as admin) see every chat, mirroring the core chat "see all"
// mode.

const (
	mcpServerName         = "open-chat"
	mcpServerVersion      = "1.0.0"
	mcpProtocolVersion    = "2025-03-26"
	mcpDefaultLimit       = 20
	mcpMaxLimit           = 100
	mcpMessageCharLimit   = 4000
	mcpInteractionTagOpen = "opencode"
)

type mcpJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *mcpJSONRPCError `json:"error,omitempty"`
}

const (
	mcpErrParse          = -32700
	mcpErrInvalidRequest = -32600
	mcpErrMethodNotFound = -32601
	mcpErrInvalidParams  = -32602
	mcpErrInternal       = -32603
)

type mcpToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPServerHandler handles the Open-Chat MCP server endpoint.
type MCPServerHandler struct{}

// HandleMCP implements the MCP HTTP-streamable transport for the Open-Chat MCP
// server. It accepts either a single JSON-RPC request (application/json) or a
// newline-delimited stream of requests and answers accordingly.
//
//	@Summary      Open-Chat MCP server
//	@Description  MCP (Model Context Protocol) JSON-RPC server exposing read-only interaction history tools scoped to the authenticated user (admins see all chats).
//	@Tags         tools
//	@Accept       json
//	@Produce      json
//	@Success      200 {object} mcpJSONRPCResponse
//	@Failure      403 {string} string "Forbidden"
//	@Router       /api/v1/mcp [post]
func (h *MCPServerHandler) HandleMCP(w http.ResponseWriter, r *http.Request) {
	db, user, err := util.GetDBAndUser(r)
	if err != nil || user == nil {
		http.Error(w, "Unable to get database or user", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeMCPJSON(w, http.StatusBadRequest, mcpJSONRPCResponse{
			JSONRPC: "2.0",
			Error:   &mcpJSONRPCError{Code: mcpErrParse, Message: "unable to read request body"},
		})
		return
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		writeMCPJSON(w, http.StatusBadRequest, mcpJSONRPCResponse{
			JSONRPC: "2.0",
			Error:   &mcpJSONRPCError{Code: mcpErrInvalidRequest, Message: "empty request body"},
		})
		return
	}

	// NDJSON stream: answer with a newline-delimited stream of responses.
	if !strings.HasPrefix(trimmed, "{") || strings.Contains(trimmed, "\n") {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			resp := h.dispatch(db, user, []byte(line))
			if resp == nil {
				continue
			}
			encoded, _ := json.Marshal(resp)
			_, _ = w.Write(append(encoded, '\n'))
			if flusher != nil {
				flusher.Flush()
			}
		}
		return
	}

	resp := h.dispatch(db, user, []byte(trimmed))
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeMCPJSON(w, http.StatusOK, *resp)
}

// dispatch parses and executes one JSON-RPC request. It returns nil for
// notifications (no response is expected).
func (h *MCPServerHandler) dispatch(db *gorm.DB, user *database.User, raw []byte) *mcpJSONRPCResponse {
	var req mcpJSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &mcpJSONRPCResponse{JSONRPC: "2.0", Error: &mcpJSONRPCError{Code: mcpErrParse, Message: "invalid JSON"}}
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpJSONRPCError{Code: mcpErrInvalidRequest, Message: "invalid JSON-RPC 2.0 request"}}
	}

	switch req.Method {
	case "initialize":
		return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": false}},
			"serverInfo":      map[string]interface{}{"name": mcpServerName, "version": mcpServerVersion},
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "ping":
		return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	case "tools/list":
		return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"tools": mcpInteractionTools()}}
	case "tools/call":
		return h.callTool(db, user, req)
	default:
		return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpJSONRPCError{Code: mcpErrMethodNotFound, Message: "unknown method: " + req.Method}}
	}
}

func (h *MCPServerHandler) callTool(db *gorm.DB, user *database.User, req mcpJSONRPCRequest) *mcpJSONRPCResponse {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpJSONRPCError{Code: mcpErrInvalidParams, Message: "invalid tool call params"}}
		}
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpJSONRPCError{Code: mcpErrInvalidParams, Message: "tool name is required"}}
	}

	result, err := runInteractionTool(db, user, name, params.Arguments)
	if err != nil {
		// Tool execution failures are reported as MCP tool errors (isError),
		// not JSON-RPC protocol errors, so the agent can read and recover.
		return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": err.Error()}},
			"isError": true,
		}}
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	return &mcpJSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": string(encoded)}},
		"isError": false,
	}}
}

func writeMCPJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// mcpInteractionTools returns the tool catalog exposed by the Open-Chat MCP
// server. All tools are read-only.
func mcpInteractionTools() []mcpToolDefinition {
	filterProps := map[string]interface{}{
		"repo": map[string]interface{}{
			"type":        "string",
			"description": "Filter by workspace repository (matches the repo:<name> tag, eg \"open-chat-go\").",
		},
		"project": map[string]interface{}{
			"type":        "string",
			"description": "Alias of repo: filter by opencode project / repository.",
		},
		"chat_type": map[string]interface{}{
			"type":        "string",
			"description": "Filter by chat type, eg \"interaction\" or \"conversation\".",
		},
		"tag": map[string]interface{}{
			"type":        "string",
			"description": "Filter by an exact chat tag, eg \"opencode\" or \"via:assign\".",
		},
		"agent": map[string]interface{}{
			"type":        "string",
			"description": "Filter by the automated participant's username (the coding agent that ran the interaction).",
		},
		"limit": map[string]interface{}{
			"type":        "integer",
			"description": fmt.Sprintf("Maximum rows to return (default %d, max %d).", mcpDefaultLimit, mcpMaxLimit),
		},
		"offset": map[string]interface{}{
			"type":        "integer",
			"description": "Rows to skip for pagination.",
		},
	}
	withRequired := func(extra map[string]interface{}, required ...string) map[string]interface{} {
		props := map[string]interface{}{}
		for k, v := range filterProps {
			props[k] = v
		}
		for k, v := range extra {
			props[k] = v
		}
		return map[string]interface{}{"type": "object", "properties": props, "required": required}
	}

	return []mcpToolDefinition{
		{
			Name:        "openchat_list_interactions",
			Description: "List the requesting user's Open-Chat interactions, most recently updated first. Admins see every interaction. Filter by repository (repo/project), chat type, tag or agent.",
			InputSchema: withRequired(nil),
		},
		{
			Name:        "openchat_search_interactions",
			Description: "Search Open-Chat interactions by a free-text query over their title, label and tags. Returns matching interactions with their repository, trigger and latest activity.",
			InputSchema: withRequired(map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Free-text query to match against chat title, label and tags."},
			}, "query"),
		},
		{
			Name:        "openchat_get_interaction",
			Description: "Get one interaction by chat UUID: its tags (repo/trigger), participants, latest message and current state.",
			InputSchema: withRequired(map[string]interface{}{
				"chat_uuid": map[string]interface{}{"type": "string", "description": "UUID of the interaction chat."},
			}, "chat_uuid"),
		},
		{
			Name:        "openchat_list_interaction_messages",
			Description: "List the messages of one interaction (oldest first) so an agent can read the full transcript and the decisions taken.",
			InputSchema: withRequired(map[string]interface{}{
				"chat_uuid": map[string]interface{}{"type": "string", "description": "UUID of the interaction chat."},
			}, "chat_uuid"),
		},
		{
			Name:        "openchat_search_interaction_messages",
			Description: "Search the requesting user's interaction messages by free text, optionally limited to one interaction or repository. Returns matching messages with their interaction.",
			InputSchema: withRequired(map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Free-text query to match against message text."},
			}, "query"),
		},
	}
}

func runInteractionTool(db *gorm.DB, user *database.User, name string, args map[string]interface{}) (interface{}, error) {
	switch name {
	case "openchat_list_interactions":
		return toolListInteractions(db, user, args)
	case "openchat_search_interactions":
		return toolSearchInteractions(db, user, args)
	case "openchat_get_interaction":
		return toolGetInteraction(db, user, args)
	case "openchat_list_interaction_messages":
		return toolListInteractionMessages(db, user, args)
	case "openchat_search_interaction_messages":
		return toolSearchInteractionMessages(db, user, args)
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func mcpArgString(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func mcpArgInt(args map[string]interface{}, key string, fallback, max int) int {
	if args == nil {
		return fallback
	}
	value := 0
	switch typed := args[key].(type) {
	case int:
		value = typed
	case int64:
		value = int(typed)
	case float64:
		value = int(typed)
	case string:
		_, _ = fmt.Sscanf(strings.TrimSpace(typed), "%d", &value)
	}
	if value <= 0 {
		return fallback
	}
	if max > 0 && value > max {
		return max
	}
	return value
}

// scopedInteractionsQuery applies the caller's visibility scope: admins see
// every chat, regular users only chats they participate in.
func scopedInteractionsQuery(db *gorm.DB, user *database.User) *gorm.DB {
	query := db.Model(&database.Chat{})
	if user != nil && user.IsAdmin {
		return query
	}
	return query.Where("(user1_id = ? OR user2_id = ?)", user.ID, user.ID)
}

// applyInteractionFilters narrows an interaction query by the shared filter
// arguments. Repository matching is done against the repo:<slug> chat tag set
// by the opencode/git integrations.
func applyInteractionFilters(db *gorm.DB, query *gorm.DB, args map[string]interface{}) *gorm.DB {
	chatType := mcpArgString(args, "chat_type")
	if chatType != "" {
		if strings.HasSuffix(chatType, "*") {
			query = query.Where("chat_type LIKE ?", strings.TrimSuffix(chatType, "*")+"%")
		} else {
			query = query.Where("chat_type = ?", chatType)
		}
	}
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
	if agent := mcpArgString(args, "agent"); agent != "" {
		sub := db.Model(&database.User{}).Select("id").Where("username = ?", agent)
		query = query.Where("(user1_id IN (?) OR user2_id IN (?))", sub, sub)
	}
	return query
}

type interactionRow struct {
	UUID            string     `json:"uuid"`
	ChatType        string     `json:"chat_type"`
	Tags            []string   `json:"tags"`
	Title           string     `json:"title,omitempty"`
	Label           string     `json:"label,omitempty"`
	Agent           string     `json:"agent,omitempty"`
	Participants    []string   `json:"participants"`
	LatestMessageAt *time.Time `json:"latest_message_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	State           string     `json:"state,omitempty"`
}

func buildInteractionRows(db *gorm.DB, user *database.User, chats []database.Chat) []interactionRow {
	rows := make([]interactionRow, 0, len(chats))
	for _, chat := range chats {
		rows = append(rows, buildInteractionRow(db, user, chat))
	}
	return rows
}

func buildInteractionRow(db *gorm.DB, user *database.User, chat database.Chat) interactionRow {
	tags := []string(chat.Tags)
	if tags == nil {
		tags = []string{}
	}
	participants := []string{}
	agent := ""
	for _, participant := range []database.User{chat.User1, chat.User2} {
		if strings.TrimSpace(participant.Name) != "" {
			participants = append(participants, participant.Name)
		}
		if participant.IsAutomated && agent == "" {
			if strings.TrimSpace(participant.Username) != "" {
				agent = participant.Username
			} else {
				agent = participant.Name
			}
		}
	}
	latestAt := &chat.UpdatedAt
	if chat.LatestMessage != nil {
		latestAt = &chat.LatestMessage.CreatedAt
	}
	title, label := interactionTitleAndLabel(db, chat)
	return interactionRow{
		UUID:            chat.UUID,
		ChatType:        chat.ChatType,
		Tags:            tags,
		Title:           title,
		Label:           label,
		Agent:           agent,
		Participants:    participants,
		LatestMessageAt: latestAt,
		UpdatedAt:       chat.UpdatedAt,
		State:           interactionState(chat),
	}
}

// interactionState reports a coarse, agent-friendly state derived from the
// latest message: finished when the last automated message is complete,
// otherwise running. An empty state means no message yet.
func interactionState(chat database.Chat) string {
	if chat.LatestMessage == nil {
		return ""
	}
	if !chat.LatestMessage.Sender.IsAutomated {
		return "waiting"
	}
	if chat.LatestMessage.DataType == "streaming" {
		return "running"
	}
	return "finished"
}

// interactionTitleAndLabel resolves the user-visible title (from ChatSettings)
// and the automation label (from the automation_chat_labels table) for a chat.
// Both are best-effort: missing values are returned empty.
func interactionTitleAndLabel(db *gorm.DB, chat database.Chat) (string, string) {
	title := ""
	settings := database.ChatSettings{}
	if err := db.Where("chat_id = ?", chat.ID).First(&settings).Error; err == nil && len(settings.ConfigData) > 0 {
		cfg := map[string]interface{}{}
		if json.Unmarshal(settings.ConfigData, &cfg) == nil {
			if value, _ := cfg["title"].(string); strings.TrimSpace(value) != "" {
				title = strings.TrimSpace(value)
			}
		}
	}
	label := ""
	var row struct {
		Label string `gorm:"column:label"`
	}
	if err := db.Table("automation_chat_labels").Select("label").Where("chat_uuid = ?", chat.UUID).Take(&row).Error; err == nil {
		label = strings.TrimSpace(row.Label)
	}
	return title, label
}

func toolListInteractions(db *gorm.DB, user *database.User, args map[string]interface{}) (interface{}, error) {
	limit := mcpArgInt(args, "limit", mcpDefaultLimit, mcpMaxLimit)
	offset := mcpArgInt(args, "offset", 0, 0)
	if offset < 0 {
		offset = 0
	}
	query := applyInteractionFilters(db, scopedInteractionsQuery(db, user), args)
	chats := []database.Chat{}
	if err := query.
		Preload("User1").
		Preload("User2").
		Preload("LatestMessage").
		Preload("LatestMessage.Sender").
		Order("updated_at desc, id desc").
		Limit(limit).
		Offset(offset).
		Find(&chats).Error; err != nil {
		return nil, fmt.Errorf("failed to list interactions: %w", err)
	}
	return map[string]interface{}{"rows": buildInteractionRows(db, user, chats), "count": len(chats)}, nil
}

func toolSearchInteractions(db *gorm.DB, user *database.User, args map[string]interface{}) (interface{}, error) {
	search := mcpArgString(args, "query")
	if search == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := mcpArgInt(args, "limit", mcpDefaultLimit, mcpMaxLimit)
	offset := mcpArgInt(args, "offset", 0, 0)
	if offset < 0 {
		offset = 0
	}
	like := "%" + search + "%"
	titleSubquery := db.Model(&database.ChatSettings{}).
		Select("chat_id").
		Where("CAST(config_data AS TEXT) LIKE ?", like)
	labelSubquery := db.Table("automation_chat_labels").
		Select("chat_uuid").
		Where("label LIKE ?", like)
	query := applyInteractionFilters(db, scopedInteractionsQuery(db, user), args).
		Where("id IN (?) OR uuid IN (?) OR CAST(tags AS TEXT) LIKE ?", titleSubquery, labelSubquery, like)
	chats := []database.Chat{}
	if err := query.
		Preload("User1").
		Preload("User2").
		Preload("LatestMessage").
		Preload("LatestMessage.Sender").
		Order("updated_at desc, id desc").
		Limit(limit).
		Offset(offset).
		Find(&chats).Error; err != nil {
		return nil, fmt.Errorf("failed to search interactions: %w", err)
	}
	return map[string]interface{}{"rows": buildInteractionRows(db, user, chats), "count": len(chats)}, nil
}

func toolGetInteraction(db *gorm.DB, user *database.User, args map[string]interface{}) (interface{}, error) {
	chatUUID := mcpArgString(args, "chat_uuid")
	if chatUUID == "" {
		return nil, fmt.Errorf("chat_uuid is required")
	}
	chat, err := loadScopedInteraction(db, user, chatUUID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"interaction": buildInteractionRow(db, user, chat)}, nil
}

func toolListInteractionMessages(db *gorm.DB, user *database.User, args map[string]interface{}) (interface{}, error) {
	chatUUID := mcpArgString(args, "chat_uuid")
	if chatUUID == "" {
		return nil, fmt.Errorf("chat_uuid is required")
	}
	chat, err := loadScopedInteraction(db, user, chatUUID)
	if err != nil {
		return nil, err
	}
	limit := mcpArgInt(args, "limit", mcpMaxLimit, mcpMaxLimit)
	offset := mcpArgInt(args, "offset", 0, 0)
	if offset < 0 {
		offset = 0
	}
	messages := []database.Message{}
	if err := db.Preload("Sender").
		Where("chat_id = ?", chat.ID).
		Order("created_at asc, id asc").
		Limit(limit).
		Offset(offset).
		Find(&messages).Error; err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	return map[string]interface{}{
		"chat_uuid": chat.UUID,
		"rows":      buildInteractionMessageRows(messages),
		"count":     len(messages),
	}, nil
}

func toolSearchInteractionMessages(db *gorm.DB, user *database.User, args map[string]interface{}) (interface{}, error) {
	search := mcpArgString(args, "query")
	if search == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := mcpArgInt(args, "limit", mcpDefaultLimit, mcpMaxLimit)
	offset := mcpArgInt(args, "offset", 0, 0)
	if offset < 0 {
		offset = 0
	}

	query := scopedInteractionsQuery(db, user)
	if repo := mcpArgString(args, "repo"); repo != "" {
		query = query.Where("CAST(tags AS TEXT) LIKE ?", "%repo:"+repo+"%")
	}
	if project := mcpArgString(args, "project"); project != "" {
		query = query.Where("CAST(tags AS TEXT) LIKE ?", "%repo:"+project+"%")
	}
	if tag := mcpArgString(args, "tag"); tag != "" {
		query = query.Where("CAST(tags AS TEXT) LIKE ?", "%"+tag+"%")
	}
	if chatUUID := mcpArgString(args, "chat_uuid"); chatUUID != "" {
		chat, err := loadScopedInteraction(db, user, chatUUID)
		if err != nil {
			return nil, err
		}
		query = query.Where("id = ?", chat.ID)
	}

	messages := []database.Message{}
	if err := db.Preload("Sender").
		Where("chat_id IN (?)", query.Select("id")).
		Where("text IS NOT NULL AND text <> ''").
		Where("CAST(text AS TEXT) LIKE ?", "%"+search+"%").
		Order("created_at desc, id desc").
		Limit(limit).
		Offset(offset).
		Find(&messages).Error; err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}

	chatIDs := make([]uint, 0, len(messages))
	seen := map[uint]struct{}{}
	for _, message := range messages {
		if _, ok := seen[message.ChatId]; ok {
			continue
		}
		seen[message.ChatId] = struct{}{}
		chatIDs = append(chatIDs, message.ChatId)
	}
	chats := []database.Chat{}
	_ = db.Where("id IN ?", chatIDs).Find(&chats).Error
	chatUUIDByID := map[uint]string{}
	for _, chat := range chats {
		chatUUIDByID[chat.ID] = chat.UUID
	}
	rows := buildInteractionMessageRows(messages)
	for i := range rows {
		rows[i].ChatUUID = chatUUIDByID[messages[i].ChatId]
	}
	return map[string]interface{}{"rows": rows, "count": len(rows)}, nil
}

type interactionMessageRow struct {
	UUID        string    `json:"uuid"`
	ChatUUID    string    `json:"chat_uuid,omitempty"`
	Sender      string    `json:"sender,omitempty"`
	IsAutomated bool      `json:"is_automated"`
	Text        string    `json:"text"`
	CreatedAt   time.Time `json:"created_at"`
}

func buildInteractionMessageRows(messages []database.Message) []interactionMessageRow {
	rows := make([]interactionMessageRow, 0, len(messages))
	for _, message := range messages {
		text := ""
		if message.Text != nil {
			text = truncateMCPText(strings.TrimSpace(*message.Text), mcpMessageCharLimit)
		}
		rows = append(rows, interactionMessageRow{
			UUID:        message.UUID,
			Sender:      message.Sender.Name,
			IsAutomated: message.Sender.IsAutomated,
			Text:        text,
			CreatedAt:   message.CreatedAt,
		})
	}
	return rows
}

func loadScopedInteraction(db *gorm.DB, user *database.User, chatUUID string) (database.Chat, error) {
	chat := database.Chat{}
	query := scopedInteractionsQuery(db, user).Where("uuid = ?", chatUUID)
	if err := query.
		Preload("User1").
		Preload("User2").
		Preload("LatestMessage").
		Preload("LatestMessage.Sender").
		First(&chat).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return chat, fmt.Errorf("interaction %s not found or not accessible", chatUUID)
		}
		return chat, fmt.Errorf("failed to load interaction: %w", err)
	}
	return chat, nil
}

func truncateMCPText(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "\n\n_(truncated by open-chat MCP server)_"
}
