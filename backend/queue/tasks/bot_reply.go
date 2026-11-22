package tasks

import (
	"backend/api/msgmate"
	wsapi "backend/api/websocket"
	"backend/botloop"
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

// asynqTaskID reports the asynq task ID of the consumed task, or "-" when the
// handler runs outside a worker (unit tests call HandleBotReply directly).
// ci#1163: the id is what the dispatch-side line logs, so the two lines can be
// joined in the log to prove the queued task really reached the turn.
func asynqTaskID(ctx context.Context) string {
	if ctx != nil {
		if id, ok := asynq.GetTaskID(ctx); ok && id != "" {
			return id
		}
	}
	return "-"
}

// asynqQueueName reports the queue the consumed task came from, or "-".
func asynqQueueName(ctx context.Context) string {
	if ctx != nil {
		if queue, ok := asynq.GetQueueName(ctx); ok && queue != "" {
			return queue
		}
	}
	return "-"
}

// asynqRetryCount reports how often the task was retried before this attempt,
// or 0. ci#1163: a stalled chat whose continuation is being retried in a loop
// looks the same as one that was never consumed unless the attempt is logged.
func asynqRetryCount(ctx context.Context) int {
	if ctx != nil {
		if n, ok := asynq.GetRetryCount(ctx); ok {
			return n
		}
	}
	return 0
}

// botReplyGenerationStartLine renders the ci#1162 pre-generation trace. The
// remaining turn budget is part of it because the private-sandbox harness gives
// up after 720s while a bot reply task may run for 30m: a continuation turn that
// starts with a nearly exhausted budget stalls the interaction without any
// further log line, which is exactly the failure relayed in ci#1162.
func botReplyGenerationStartLine(chatUUID string, ctx context.Context) string {
	return fmt.Sprintf(
		"bot-reply generation start: chat=%s budget=%s",
		chatUUID, botReplyTurnBudget(ctx).Round(time.Second),
	)
}

// botReplyGenerationFinishedLine renders the ci#1162 post-generation trace so
// the generation window is bracketed and a missing pair is unambiguous.
func botReplyGenerationFinishedLine(chatUUID string, err error) string {
	return fmt.Sprintf("bot-reply generation finished: chat=%s ok=%t", chatUUID, err == nil)
}

// @doc:open-chat-hal-agent-logic
// Open-Chat HAL agent logic runs as the `bot:reply` async task. It validates
// the bot/sender/message context, creates a short-lived bot session, rebuilds
// websocket-style message payload (including attachments metadata), and then
// calls the AI handler to generate and persist/send the bot response.
func HandleBotReply(ctx context.Context, task *asynq.Task, deps Deps) error {
	// ci#1163: the consume-side counterpart of the dispatch log in
	// workqueue.bot_reply. The private-sandbox stalls showed a chat whose
	// confirmable action reported continuation_queued and which then stayed
	// active with no bot turn until the harness reaped it; from the log alone
	// "the worker never picked the task up" and "the worker picked it up and the
	// turn failed before doing anything" look identical. This line is therefore
	// emitted first, before any state that can make the turn bail out, so its
	// absence/presence alone tells the two apart.
	logging.Infof(
		"bot-reply turn start (ci#1163): consuming queued reply (task id=%s type=%s queue=%s retry=%d), attempting payload decode",
		asynqTaskID(ctx), task.Type(), asynqQueueName(ctx), asynqRetryCount(ctx),
	)
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

	// ci#1153: a continuation turn that is queued but never reaches the agent
	// leaves the chat stalled with nothing in the log that separates "the task
	// was never dequeued" from "the handler ran and dropped the turn". The
	// confirmable-action side already logs the enqueue (ci#1147); this is the
	// matching consumer-side line, keyed to the task ID and the source message,
	// so a relayed stall is attributable from the log alone.
	//
	// asynq.Task carries no ID (the ID lives in the handler context), so read it
	// from the context; a handler invoked without one still logs the turn.
	taskID, hasTaskID := asynq.GetTaskID(ctx)
	logging.Infof(
		"bot-reply task start: chat=%s task=%s task_id_known=%v message=%s sender_automated=%v continuation=%v",
		payload.ChatUUID, taskID, hasTaskID, incomingMessage.UUID, senderUser.IsAutomated,
		strings.Contains(taskID, ":continuation"),
	)

	// ci#747 RC-8: refuse to add another turn to a bot-to-bot loop. Only an
	// automated peer can loop, so the check is gated on that; a long
	// all-automated run, a run of identical messages, or a burst of turns
	// seconds apart stops the spin (the peer never gets a reply, so the loop
	// ends). Legitimate slow agent-to-agent coordination never trips it, and a
	// single automated sender that is still making progress (ci#915) is treated
	// as an ongoing run rather than a loop. When the breaker does refuse a
	// mid-round coder run, surface the reason in the task result so the
	// per-run agent (a msgmate_coder_* probe) sees a concrete failure instead
	// of a silent completion it can only attribute to its own tool output.
	if senderUser.IsAutomated {
		if decision := botloop.DetectForChat(deps.DB, incomingMessage.ChatId, botloop.ConfigFromEnv(), time.Now()); decision.Loop {
			logging.Warnf("bot-reply loop breaker (ci#747): refusing to answer chat %s (%s: %s)", payload.ChatUUID, decision.Reason, decision.Detail)
			failure := ToolExecutionResult{Success: false, Error: "bot-reply loop breaker: " + decision.Reason + ": " + decision.Detail}
			_ = writeResult(task, failure)
			persistTaskResult(deps.DB, task, failure)
			return fmt.Errorf("bot reply loop breaker (%s): %w", decision.Reason, asynq.SkipRetry)
		}
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

	// ci#1162: the continuation turn of a stalled chat re-enters the handler and
	// then produces no further line until the turn ends, so a relayed
	// "interaction timed out ... state=active" cannot be attributed to the
	// pre-generation setup or to the generation/tool loop itself. Bracket the
	// call, and record the remaining turn budget: the private-sandbox harness
	// gives up long before the 30m task timeout, so a starving turn (tiny
	// remaining budget on a retry/continuation) has to be visible in the log.
	//
	// ci#1161: the bracketing pair also carries the task id, the continuation
	// flag and the incoming message uuid, because the failing private coder
	// probes showed "task start", two chat fetches, one message list and then
	// nothing until the harness timed out. A missing "generation finished" line
	// therefore means the turn is blocked inside the handler, a present one
	// means the handler returned and the stall is downstream of it. Both tickets
	// are folded into one pair of lines so a log never carries two competing
	// generation-start events for the same turn.
	generationStartedAt := time.Now()
	continuationTurn := strings.Contains(taskID, ":continuation")
	logging.Infof(
		"%s task=%s continuation=%v message=%s",
		botReplyGenerationStartLine(payload.ChatUUID, ctx),
		taskID, continuationTurn, incomingMessage.UUID,
	)
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
	logging.Infof(
		"%s task=%s continuation=%v elapsed=%s err=%v",
		botReplyGenerationFinishedLine(payload.ChatUUID, err),
		taskID, continuationTurn, time.Since(generationStartedAt).Round(time.Millisecond), err,
	)
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
	case msgmate.FailureReasonProviderBackpressure:
		// ci#909: provider admission saturation (the per-key slot cap was
		// exhausted and no slot freed up within the queue timeout, so the
		// request never reached the provider) is OpenChat's own concurrency
		// back-pressure, never a provider fault. It must not suggest switching
		// model/provider or blame credits/outage.
		return "Too many AI requests are queued for this provider key right now, so this reply was stopped before reaching the AI provider. Please lower the number of concurrent requests (or wait a moment) and try again. The work already done is kept."
	case msgmate.FailureReasonTimeout:
		return "This turn ran longer than the server's time limit for a single reply and was stopped. The work already done is kept - send another message to continue from here."
	case msgmate.FailureReasonToolCallBudget:
		// ci#1160: a reached tool-call cap is a capacity stop, not an internal
		// fault - the server ended the turn exactly as designed. Reporting it
		// as "internal error" told the user (and the agent self-check) the
		// server broke when it actually protected itself; the cap guidance
		// explains the stop and how to continue.
		return "It stopped because this chat's tool-call budget was exhausted. The work already done (tool calls, files, commits) is kept - send another message to continue from here."
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

// botFailureMessageMetadata builds the metadata of the failure message a bot
// posts when its generation turn did not complete.
//
// ci#1160: a capacity stop is a stop the server performed on itself by design
// (tool-call budget exhausted, or provider admission saturation so the request
// never reached the provider). It is not an internal fault, so it must not
// carry the error flag: the chat UI and the ci#622 native-coder self-check read
// `error: true` as "the server broke" and settled every role's interaction as
// state=failed, which is what turned the per-role suite red 0/5.
func botFailureMessageMetadata(err error) map[string]interface{} {
	metadata := map[string]interface{}{
		"finished": true,
		"error":    true,
	}
	if err == nil {
		return metadata
	}
	classification := msgmate.ClassifyGenerationFailure(err)
	metadata["failure_reason"] = classification.Reason
	metadata["error_detail"] = err.Error()
	if msgmate.IsCapacityStopReason(classification.Reason) {
		delete(metadata, "error")
		metadata["capacity_stop"] = classification.Reason
	}
	if classification.ProviderSummary != "" {
		metadata["provider_error"] = classification.ProviderSummary
	}
	return metadata
}

func sendBotFailureMessage(ocClient *client.Client, chatUUID, text string, err error) error {
	metadata := botFailureMessageMetadata(err)

	return ocClient.SendChatMessage(chatUUID, client.SendMessage{
		Text:     text,
		MetaData: &metadata,
	})
}
