package tasks

import (
	"backend/api/msgmate"
	wsapi "backend/api/websocket"
	"backend/database"
	"backend/workqueue"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	client "github.com/msgmate-io/go-client-integration/goclient"
	"gorm.io/gorm"

	"backend/logging"
)

// botReplyTurnBudget resolves how long the current turn may still run: the
// remaining task deadline when asynq set one, otherwise the configured bot reply
// turn timeout. It is the basis of the bot session lifetime so the two can never
// drift apart again (ci#729).
func botReplyTurnBudget(ctx context.Context) time.Duration {
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining > 0 {
				return remaining
			}
		}
	}
	return workqueue.BotReplyTimeout()
}

// @doc:open-chat-hal-agent-logic
// Open-Chat HAL agent logic runs as the `bot:reply` async task. It validates
// the bot/sender/message context, creates a short-lived bot session, rebuilds
// websocket-style message payload (including attachments metadata), and then
// calls the AI handler to generate and persist/send the bot response.
func HandleBotReply(ctx context.Context, task *asynq.Task, deps Deps) error {
	if deps.DB == nil {
		return fmt.Errorf("%w: database unavailable", asynq.SkipRetry)
	}

	var payload workqueue.BotReplyPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: invalid payload: %v", asynq.SkipRetry, err)
	}

	if payload.ChatUUID == "" || payload.MessageUUID == "" || payload.BotUserID == 0 {
		return fmt.Errorf("%w: chat_uuid, message_uuid and bot_user_id are required", asynq.SkipRetry)
	}

	var botUser database.User
	if err := deps.DB.First(&botUser, "id = ?", payload.BotUserID).Error; err != nil {
		return fmt.Errorf("%w: bot user not found", asynq.SkipRetry)
	}
	if !botUser.IsAutomated {
		return fmt.Errorf("%w: receiver is not an automated user", asynq.SkipRetry)
	}

	var incomingMessage database.Message
	if err := deps.DB.Where("uuid = ?", payload.MessageUUID).First(&incomingMessage).Error; err != nil {
		return fmt.Errorf("%w: source message not found", asynq.SkipRetry)
	}

	// ci#656: this task is the consumer of the message, so the reply is no
	// longer queued behind a live turn. Drop the marker the transcript renders
	// as pending before the metadata is forwarded, so a consumed message never
	// reaches the agent looking queued - on the success path and on every
	// failure/retry path alike, because the drain happens before the turn.
	if _, clearErr := database.ClearMessageQueued(deps.DB, &incomingMessage); clearErr != nil {
		logging.Errorf("ci#656: failed to clear the queued marker on message %s: %v", incomingMessage.UUID, clearErr)
	}

	var senderUser database.User
	if err := deps.DB.First(&senderUser, "id = ?", incomingMessage.SenderId).Error; err != nil {
		return fmt.Errorf("%w: sender not found", asynq.SkipRetry)
	}

	// The session must outlive the whole turn, not just a fixed 15 minutes:
	// derive its lifetime from the turn budget and keep renewing it while the
	// turn runs (ci#729).
	token := uuid.NewString()
	sessionTTL := database.SessionTTL(botReplyTurnBudget(ctx))
	session := database.Session{
		UserId: botUser.ID,
		Token:  token,
		Expiry: time.Now().Add(sessionTTL),
	}
	if err := deps.DB.Create(&session).Error; err != nil {
		return fmt.Errorf("%w: failed to create bot session", asynq.SkipRetry)
	}
	defer deps.DB.Where("token = ?", token).Delete(&database.Session{})
	stopSessionRefresh := database.StartSessionRefresh(deps.DB, token, sessionTTL)
	defer stopSessionRefresh()

	// The fallback lives in workqueue alongside the worker flag's default so the
	// two can never drift apart (it is only reachable when BackendHost is empty).
	ocClient := client.NewClient(workqueue.BackendHostOrDefault(deps.BackendHost))
	ocClient.SetSessionId(token)
	ocClient.User = client.User{UUID: botUser.UUID}

	wsHandler := deps.WSHandler
	if wsHandler == nil {
		wsHandler = wsapi.NewWebSocketHandler()
	}

	botContext := &msgmate.BotContext{
		Client:    ocClient,
		BotUser:   botUser,
		WSHandler: wsHandler,
	}

	message := wsapi.NewMessage{Type: "new_message"}
	message.Content.ChatUUID = payload.ChatUUID
	message.Content.SenderUUID = senderUser.UUID
	if incomingMessage.Text != nil {
		message.Content.Text = *incomingMessage.Text
	}
	if incomingMessage.Reasoning != nil {
		message.Content.Reasoning = *incomingMessage.Reasoning
	}

	if len(incomingMessage.MetaData) > 0 {
		var meta map[string]interface{}
		if err := json.Unmarshal(incomingMessage.MetaData, &meta); err == nil {
			message.Content.MetaData = &meta
			if rawAttachments, ok := meta["attachments"].([]interface{}); ok {
				attachments := make([]wsapi.FileAttachment, 0, len(rawAttachments))
				for _, rawAttachment := range rawAttachments {
					attachmentMap, ok := rawAttachment.(map[string]interface{})
					if !ok {
						continue
					}
					attachment := wsapi.FileAttachment{}
					if fileID, ok := attachmentMap["file_id"].(string); ok {
						attachment.FileID = fileID
					}
					if mimeType, ok := attachmentMap["mime_type"].(string); ok {
						attachment.MimeType = mimeType
					}
					attachments = append(attachments, attachment)
				}
				if len(attachments) > 0 {
					message.Content.Attachments = &attachments
				}
			}
		}
	}

	var err error
	if backendFn, config, ok := msgmate.ResolveChatBackend(deps.DB, payload.ChatUUID); ok {
		err = backendFn(ctx, msgmate.ChatBackendRequest{
			DB:         deps.DB,
			BotContext: botContext,
			Message:    message,
			Config:     config,
		})
	} else {
		err = msgmate.NewAIHandler(botContext).GenerateResponse(ctx, message)
	}
	if err != nil {
		responseAlreadySent := errors.Is(err, msgmate.ErrResponseAlreadySent)
		if isBotReplyCancellation(err, ctx) {
			failure := ToolExecutionResult{Success: false, Error: botReplyFailureMessage(context.Canceled)}
			_ = writeResult(task, failure)
			persistTaskResult(deps.DB, task, failure)
			return fmt.Errorf("bot reply interrupted: %w", asynq.SkipRetry)
		}

		failureMessage := botReplyFailureMessage(err)
		if !responseAlreadySent {
			if sendErr := sendBotFailureMessage(ocClient, payload.ChatUUID, failureMessage, err); sendErr != nil {
				failureMessage = fmt.Sprintf("%s (fallback send failed: %v)", failureMessage, sendErr)
			}
		}
		if responseAlreadySent {
			if disposeErr := disposeToolInitAfterBotReply(deps.DB, payload.ChatUUID); disposeErr != nil {
				logging.Errorf("tool_init disposal failed after already-sent response for chat %s: %v", payload.ChatUUID, disposeErr)
			}
			failureMessage = fmt.Sprintf("%s (response already sent)", failureMessage)
		}
		failure := ToolExecutionResult{Success: false, Error: failureMessage}
		_ = writeResult(task, failure)
		persistTaskResult(deps.DB, task, failure)
		return fmt.Errorf("bot reply generation failed: %w", asynq.SkipRetry)
	}

	if disposeErr := disposeToolInitAfterBotReply(deps.DB, payload.ChatUUID); disposeErr != nil {
		logging.Errorf("tool_init disposal failed for chat %s: %v", payload.ChatUUID, disposeErr)
	}

	// A stop that lands after the generation finished but before the task
	// settled must not be recorded as a success: returning SkipRetry keeps a
	// cancelled task from being retried (and re-running the generation). Only a
	// genuine user stop counts here - a task deadline that fired as the reply
	// completed is not a stop, and the reply is already persisted (ci#642).
	if errors.Is(ctx.Err(), context.Canceled) {
		failure := ToolExecutionResult{Success: false, Error: botReplyFailureMessage(context.Canceled)}
		_ = writeResult(task, failure)
		persistTaskResult(deps.DB, task, failure)
		return fmt.Errorf("bot reply interrupted: %w", asynq.SkipRetry)
	}

	success := ToolExecutionResult{Success: true, Result: "bot reply generated"}
	persistTaskResult(deps.DB, task, success)
	return writeResult(task, success)
}

// isBotReplyCancellation reports whether a bot reply error is a user/queue
// cancellation rather than a generation failure. It checks both the wrapped
// error and the task context so a provider request aborted by the stop signal
// is never mistaken for a retryable failure that would re-run the generation.
// A context.Canceled stop is a cancellation; a task deadline
// (context.DeadlineExceeded) is NOT - it is a timeout that takes the failure
// path so it never renders as a user pause (ci#642).
func isBotReplyCancellation(err error, ctx context.Context) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return true
	}
	return false
}

func shouldDisposeToolInitForChat(chat database.Chat) bool {
	if !strings.HasPrefix(chat.ChatType, "interaction") {
		return false
	}

	if chat.SharedConfig == nil || len(chat.SharedConfig.ConfigData) == 0 {
		return true
	}

	configData := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err != nil {
		return true
	}

	rawPersist, exists := configData["persist_tool_init"]
	if !exists {
		return true
	}

	persist, ok := rawPersist.(bool)
	if !ok {
		return true
	}

	return !persist
}

func disposeToolInitAfterBotReply(DB *gorm.DB, chatUUID string) error {
	if DB == nil {
		return nil
	}

	var chat database.Chat
	if err := DB.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		return err
	}

	if !shouldDisposeToolInitForChat(chat) {
		return nil
	}

	return database.NewToolInitDataManager(DB).DisposeToolInitDataForChat(&chat)
}

func botReplyFailureMessage(err error) string {
	if err == nil {
		return "I ran into an error while generating a reply. Please try again in a moment."
	}

	if errors.Is(err, context.Canceled) {
		return "I paused this reply. Send another message when you want me to continue."
	}

	switch classification := msgmate.ClassifyGenerationFailure(err); classification.Reason {
	case msgmate.FailureReasonProviderError:
		return fmt.Sprintf("I could not generate a reply: my AI provider request failed (%s). Please try again in a moment or switch to another model.", classification.ProviderSummary)
	case msgmate.FailureReasonTransientNetwork:
		return "I can't reach my AI provider right now. Please check the provider configuration and try again."
	case msgmate.FailureReasonContextWindow:
		return "The response exceeded the model's maximum context window. Try a shorter prompt, reduce large pasted content, or run commands that produce less output."
	case msgmate.FailureReasonTimeout:
		return "This turn ran longer than the server's time limit for a single reply and was stopped. The work already done is kept - send another message to continue from here."
	}

	if isProviderReachabilityError(err) {
		return "I can't reach my AI provider right now. Please check the provider configuration and try again."
	}

	return fmt.Sprintf("I hit an internal error while generating this reply: %s", msgmate.TruncateFailureDetail(err.Error()))
}

// isProviderReachabilityError recognises provider configuration / connectivity
// problems that produce a network-level error rather than a provider HTTP
// response, so they can be described as reachability issues instead of an
// internal fault.
func isProviderReachabilityError(err error) bool {
	if err == nil {
		return false
	}
	errLower := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection refused",
		"no such host",
		"timeout",
		"api key",
		"unauthorized",
		"forbidden",
	} {
		if strings.Contains(errLower, marker) {
			return true
		}
	}
	return false
}

func sendBotFailureMessage(ocClient *client.Client, chatUUID, text string, err error) error {
	metadata := map[string]interface{}{
		"finished": true,
		"error":    true,
	}
	if err != nil {
		classification := msgmate.ClassifyGenerationFailure(err)
		metadata["failure_reason"] = classification.Reason
		metadata["error_detail"] = err.Error()
		if classification.ProviderSummary != "" {
			metadata["provider_error"] = classification.ProviderSummary
		}
	}

	return ocClient.SendChatMessage(chatUUID, client.SendMessage{
		Text:     text,
		MetaData: &metadata,
	})
}
