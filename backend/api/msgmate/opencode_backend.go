//go:build opencodeintegration

package msgmate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	wsapi "backend/api/websocket"
	"backend/database"

	"github.com/google/uuid"
	client "github.com/msgmate-io/go-client-integration/goclient"
	opencodeintegration "github.com/msgmate-io/opencode-integration"
	"gorm.io/gorm"
)

// opencodeStreamingBotSessionExpiry keeps the detached generation's bot session
// valid across long permission pauses (bounded by the integration's generation
// lifetime).
const opencodeStreamingBotSessionExpiry = 3 * time.Hour

func init() {
	RegisterChatBackend("opencode", opencodeChatBackend)
}

// opencodeChatBackend forwards the incoming chat message to the opencode session
// bound to the chat and streams the generation live. OpenCode can block on
// permission prompts for longer than the bot reply task's timeout, and the task's
// bot session is deleted when it returns, so the actual generation runs in a
// detached goroutine with its own bot session. The task returns immediately after
// spawning it.
func opencodeChatBackend(_ context.Context, req ChatBackendRequest) error {
	if req.BotContext == nil || req.BotContext.Client == nil {
		return fmt.Errorf("opencode backend requires a bot context with an API client")
	}
	chatUUID := strings.TrimSpace(req.Message.Content.ChatUUID)
	text := req.Message.Content.Text
	if chatUUID == "" {
		return fmt.Errorf("opencode backend requires a chat uuid")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("opencode backend requires message text")
	}

	projectHint := opencodeProjectHintFromConfig(req.Config)
	model := ""
	if v, ok := req.Config["model"].(string); ok {
		model = strings.TrimSpace(v)
	}

	var senderID uint
	if senderUUID := strings.TrimSpace(req.Message.Content.SenderUUID); senderUUID != "" {
		var sender database.User
		if err := req.DB.Where("uuid = ?", senderUUID).First(&sender).Error; err == nil {
			senderID = sender.ID
		}
	}

	host := req.BotContext.Client.GetHost()
	botUser := req.BotContext.BotUser
	wsHandler := req.BotContext.WSHandler
	senderUUID := req.Message.Content.SenderUUID
	db := req.DB

	go runOpencodeStreamingGeneration(db, host, botUser, wsHandler, chatUUID, senderUUID, projectHint, model, senderID, text)

	return nil
}

// opencodeProjectHintFromConfig resolves the opencode project for a chat from the
// shared config: the project-selection tool_init payload first (default bot flow),
// then a direct opencode_project key (manually configured bots).
func opencodeProjectHintFromConfig(config map[string]interface{}) string {
	if toolInit, ok := config["tool_init"].(map[string]interface{}); ok {
		if toolPayload, ok := toolInit[opencodeintegration.ToolNameSelectProject].(map[string]interface{}); ok {
			if v, ok := toolPayload["project_uuid"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	if v, ok := config["opencode_project"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// runOpencodeStreamingGeneration creates a dedicated bot session/client and drives
// a single opencode generation end to end, delivering activity through the sink.
func runOpencodeStreamingGeneration(db *gorm.DB, host string, botUser database.User, wsHandler *wsapi.WebSocketHandler, chatUUID, senderUUID, projectHint, model string, senderID uint, text string) {
	token := uuid.NewString()
	session := database.Session{
		UserId: botUser.ID,
		Token:  token,
		Expiry: time.Now().Add(opencodeStreamingBotSessionExpiry),
	}
	if err := db.Create(&session).Error; err != nil {
		log.Printf("opencode streaming: failed to create bot session for chat %s: %v", chatUUID, err)
		return
	}
	defer db.Where("token = ?", token).Delete(&database.Session{})

	ocClient := client.NewClient(host)
	ocClient.SetSessionId(token)
	ocClient.User = client.User{UUID: botUser.UUID}

	sink := &opencodeStreamSink{
		db:         db,
		chatUUID:   chatUUID,
		senderUUID: senderUUID,
		wsHandler:  wsHandler,
		client:     ocClient,
		partialID:  fmt.Sprintf("%s-%d", chatUUID, time.Now().UnixNano()),
		startTime:  time.Now(),
	}

	opts := opencodeintegration.RunOptions{
		ProjectUUIDHint: projectHint,
		Model:           model,
		OwnerUserID:     senderID,
	}
	if err := opencodeintegration.RunStreamingGeneration(context.Background(), db, chatUUID, text, sink, opts); err != nil {
		log.Printf("opencode streaming generation for chat %s ended with error: %v", chatUUID, err)
	}
}

// opencodeStreamSink implements opencodeintegration.StreamSink by pushing partial
// message websocket frames for live activity and persisting messages (permission
// prompts, the final reply, failures) as the bot user.
type opencodeStreamSink struct {
	db         *gorm.DB
	chatUUID   string
	senderUUID string
	wsHandler  *wsapi.WebSocketHandler
	client     *client.Client
	partialID  string
	startTime  time.Time

	mu      sync.Mutex
	started bool
	ended   bool
}

func (s *opencodeStreamSink) pushFrame(frame []byte) {
	if s.wsHandler == nil {
		return
	}
	s.wsHandler.MessageHandler.SendMessage(s.wsHandler, s.senderUUID, frame)
}

func (s *opencodeStreamSink) OnStart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	s.pushFrame(s.wsHandler.MessageHandler.StartPartialMessage(s.chatUUID, s.senderUUID, s.partialID))
}

// OnSessionInfo persists an instance info message as the bot's first message in
// the chat. It is rendered by the frontend as an opencode session info widget.
func (s *opencodeStreamSink) OnSessionInfo(info opencodeintegration.SessionInfo) {
	modelDisplay := strings.TrimSpace(info.Model)
	if modelDisplay == "" {
		modelDisplay = "instance default"
	}
	messageText := fmt.Sprintf("OpenCode instance connected: project %q (%s), model %s.", info.ProjectName, info.ProjectMode, modelDisplay)

	metadata := map[string]interface{}{
		"backend":  "opencode",
		"finished": true,
		"opencode_session_info": map[string]interface{}{
			"project_uuid": info.ProjectUUID,
			"project_name": info.ProjectName,
			"project_mode": info.ProjectMode,
			"project_path": info.ProjectPath,
			"session_id":   info.SessionID,
			"model":        info.Model,
			"provider_id":  info.ProviderID,
		},
	}
	if err := s.client.SendChatMessage(s.chatUUID, client.SendMessage{Text: messageText, MetaData: &metadata}); err != nil {
		log.Printf("opencode streaming: failed to persist session info for chat %s: %v", s.chatUUID, err)
	}
}

func (s *opencodeStreamSink) endPartialLocked() {
	if !s.started || s.ended {
		return
	}
	s.ended = true
	s.pushFrame(s.wsHandler.MessageHandler.EndPartialMessage(s.chatUUID, s.senderUUID, s.partialID))
}

func (s *opencodeStreamSink) pushPartial(text string, reasoning []string, meta map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.ended {
		return
	}
	s.pushFrame(s.wsHandler.MessageHandler.NewPartialMessage(s.chatUUID, s.senderUUID, s.partialID, text, reasoning, &meta, nil, nil))
}

func (s *opencodeStreamSink) elapsedMeta(phase string) map[string]interface{} {
	return map[string]interface{}{
		"partial_phase": phase,
		"backend":       "opencode",
		"total_time":    time.Since(s.startTime).Round(time.Millisecond).String(),
	}
}

func (s *opencodeStreamSink) OnTextDelta(delta string) {
	if delta == "" {
		return
	}
	s.pushPartial(delta, nil, s.elapsedMeta("responding"))
}

func (s *opencodeStreamSink) OnReasoningDelta(delta string) {
	if delta == "" {
		return
	}
	s.pushPartial("", []string{delta}, s.elapsedMeta("thinking"))
}

func (s *opencodeStreamSink) OnToolActivity(activity []map[string]interface{}) {
	items := make([]interface{}, 0, len(activity))
	for _, entry := range activity {
		items = append(items, entry)
	}
	meta := s.elapsedMeta("working")
	meta["opencode_activity"] = items
	s.pushPartial("", nil, meta)
}

func (s *opencodeStreamSink) OnPermissionRequest(perm opencodeintegration.PermissionRequest) {
	title, description := describeOpencodePermission(perm)

	permissionMeta := map[string]interface{}{
		"id":          perm.ID,
		"session_id":  perm.SessionID,
		"action":      perm.Action,
		"patterns":    perm.Patterns,
		"metadata":    perm.Metadata,
		"always":      perm.Always,
		"status":      "pending",
		"title":       title,
		"description": description,
	}
	metadata := map[string]interface{}{
		"backend":             "opencode",
		"finished":            true,
		"opencode_permission": permissionMeta,
	}

	messageText := title
	if description != "" {
		messageText = title + ": " + description
	}
	if err := s.client.SendChatMessage(s.chatUUID, client.SendMessage{Text: messageText, MetaData: &metadata}); err != nil {
		log.Printf("opencode streaming: failed to persist permission prompt for chat %s: %v", s.chatUUID, err)
	}
}

func (s *opencodeStreamSink) OnComplete(reply opencodeintegration.BridgeReply) {
	s.mu.Lock()
	s.endPartialLocked()
	s.mu.Unlock()

	replyText := strings.TrimSpace(reply.Text)
	if replyText == "" {
		replyText = "I finished that step but had no text output to share."
	}

	metadata := map[string]interface{}{
		"total_time": time.Since(s.startTime).Round(time.Millisecond).String(),
		"finished":   true,
		"backend":    "opencode",
		"opencode": map[string]interface{}{
			"session_id":  reply.SessionID,
			"message_id":  reply.MessageID,
			"model":       reply.Model,
			"provider":    reply.Provider,
			"cost":        reply.Cost,
			"finish":      reply.Finish,
			"duration_ms": reply.DurationMS,
		},
	}

	outgoing := client.SendMessage{Text: replyText, MetaData: &metadata}
	if len(reply.Reasoning) > 0 {
		outgoing.Reasoning = reply.Reasoning
	}
	if err := s.client.SendChatMessage(s.chatUUID, outgoing); err != nil {
		log.Printf("opencode streaming: failed to persist reply for chat %s: %v", s.chatUUID, err)
	}
}

func (s *opencodeStreamSink) OnError(err error) {
	s.mu.Lock()
	s.endPartialLocked()
	s.mu.Unlock()

	// A cancelled generation (typically superseded by a newer message in the same
	// chat) ends silently rather than leaving a noisy failure message behind. Any
	// permission cards it surfaced are marked cancelled so they stop being actionable.
	if errors.Is(err, context.Canceled) {
		opencodeintegration.CancelPendingChatPermissionCards(s.db, s.chatUUID)
		return
	}

	metadata := map[string]interface{}{
		"finished": true,
		"error":    true,
		"backend":  "opencode",
	}
	messageText := "I ran into an error while working on that. Please try again in a moment."
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		messageText = fmt.Sprintf("%s (%s)", messageText, strings.TrimSpace(err.Error()))
	}
	if sendErr := s.client.SendChatMessage(s.chatUUID, client.SendMessage{Text: messageText, MetaData: &metadata}); sendErr != nil {
		log.Printf("opencode streaming: failed to persist error message for chat %s: %v", s.chatUUID, sendErr)
	}
}

// describeOpencodePermission derives a short human-readable title and description
// from an opencode permission request so the chat can present it clearly.
func describeOpencodePermission(perm opencodeintegration.PermissionRequest) (string, string) {
	metaValue := func(keys ...string) string {
		for _, key := range keys {
			if v, ok := perm.Metadata[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	firstPattern := ""
	if len(perm.Patterns) > 0 {
		firstPattern = strings.TrimSpace(perm.Patterns[0])
	}

	switch strings.ToLower(strings.TrimSpace(perm.Action)) {
	case "edit":
		target := metaValue("filepath", "path")
		if target == "" {
			target = firstPattern
		}
		return "OpenCode wants to edit a file", target
	case "bash", "shell":
		command := metaValue("command", "cmd")
		return "OpenCode wants to run a command", command
	case "read":
		target := metaValue("filepath", "path")
		if target == "" {
			target = firstPattern
		}
		return "OpenCode wants to read a file", target
	case "webfetch":
		return "OpenCode wants to fetch a URL", metaValue("url")
	default:
		action := strings.TrimSpace(perm.Action)
		if action == "" {
			action = "perform an action"
		}
		return "OpenCode permission required: " + action, firstPattern
	}
}
