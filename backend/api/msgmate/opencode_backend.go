//go:build opencodeintegration

package msgmate

import (
	"context"
	"fmt"
	"strings"
	"time"

	client "github.com/msgmate-io/go-client-integration/goclient"
	opencodeintegration "github.com/msgmate-io/opencode-integration"
)

func init() {
	RegisterChatBackend("opencode", opencodeChatBackend)
}

// opencodeChatBackend forwards the incoming chat message to the opencode session
// bound to the chat (creating the runtime/session on first use), surfaces activity
// via partial-message websocket frames, and persists the assistant reply as a bot
// message so it appears in the chat history.
func opencodeChatBackend(ctx context.Context, req ChatBackendRequest) error {
	if req.BotContext == nil || req.BotContext.Client == nil {
		return fmt.Errorf("opencode backend requires a bot context with an API client")
	}
	chatUUID := req.Message.Content.ChatUUID
	senderUUID := req.Message.Content.SenderUUID
	text := req.Message.Content.Text

	projectHint := ""
	if v, ok := req.Config["opencode_project"].(string); ok {
		projectHint = strings.TrimSpace(v)
	}

	startTime := time.Now()
	partialSessionID := fmt.Sprintf("%s-%d", chatUUID, time.Now().UnixNano())

	if req.BotContext.WSHandler != nil {
		req.BotContext.WSHandler.MessageHandler.SendMessage(
			req.BotContext.WSHandler,
			senderUUID,
			req.BotContext.WSHandler.MessageHandler.StartPartialMessage(chatUUID, senderUUID, partialSessionID),
		)
	}

	reply, err := opencodeintegration.BridgeGenerateReplyForChat(req.DB, chatUUID, projectHint, text)

	if req.BotContext.WSHandler != nil {
		req.BotContext.WSHandler.MessageHandler.SendMessage(
			req.BotContext.WSHandler,
			senderUUID,
			req.BotContext.WSHandler.MessageHandler.EndPartialMessage(chatUUID, senderUUID, partialSessionID),
		)
	}

	if err != nil {
		return err
	}

	replyText := strings.TrimSpace(reply.Text)
	if replyText == "" {
		replyText = "I finished that step but had no text output to share."
	}

	metadata := map[string]interface{}{
		"total_time": time.Since(startTime).Round(time.Millisecond).String(),
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

	outgoing := client.SendMessage{
		Text:     replyText,
		MetaData: &metadata,
	}
	if len(reply.Reasoning) > 0 {
		outgoing.Reasoning = reply.Reasoning
	}
	return req.BotContext.Client.SendChatMessage(chatUUID, outgoing)
}
