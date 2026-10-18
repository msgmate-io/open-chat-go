package msgmate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	wsapi "backend/api/websocket"
	"backend/chatstate"
	client "github.com/msgmate-io/go-client-integration/goclient"
)

// providerHostEnv maps provider backends whose API host is configured through
// a dedicated environment variable. When set, the env host overrides the
// endpoint stored in the chat's shared config.
var providerHostEnv = map[string]string{
	"litellm":         "LITELLM_API_HOST",
	"msgmate_cluster": "MSGMATE_CLUSTER_HOST",
}

// resolveProviderEndpoint applies provider-specific endpoint rules: host-env
// providers (litellm, msgmate_cluster) require a resolvable host, and
// anthropic falls back to its public API when no host is configured.
func resolveProviderEndpoint(backend, endpoint string) (string, error) {
	if hostEnv, ok := providerHostEnv[backend]; ok {
		providerHost := strings.TrimSpace(os.Getenv(hostEnv))
		if providerHost != "" {
			endpoint = providerHost
		}
		endpoint = strings.TrimSpace(endpoint)
		endpoint = strings.TrimRight(endpoint, "/")
		if endpoint == "" {
			return "", fmt.Errorf("missing API host for %s provider", backend)
		}
		return endpoint, nil
	}

	if backend == "anthropic" {
		anthropicHost := strings.TrimSpace(os.Getenv("ANTHROPIC_API_HOST"))
		if anthropicHost != "" {
			endpoint = anthropicHost
		} else if strings.TrimSpace(endpoint) == "" {
			endpoint = "https://api.anthropic.com/v1"
		}
		endpoint = strings.TrimSpace(endpoint)
		endpoint = strings.TrimRight(endpoint, "/")
		if endpoint == "" {
			return "", fmt.Errorf("missing API host for anthropic provider")
		}
	}

	// Fixed public base URLs for well-known OpenAI-compatible providers when no
	// custom endpoint is configured.
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "openrouter":
		if strings.TrimSpace(endpoint) == "" {
			endpoint = "https://openrouter.ai/api/v1/"
		}
	case "ionos":
		if strings.TrimSpace(endpoint) == "" {
			endpoint = "https://openai.inference.de-txl.ionos.com/v1/"
		}
	}

	return endpoint, nil
}

// resolveChatProviderEndpoint resolves the effective provider endpoint for a
// legacy (non-opencode) chat config. It first applies the provider-specific
// rules (env hosts and the public defaults for openrouter/ionos/anthropic) and
// only then falls back to the in-cluster localai endpoint, so a public provider
// such as openrouter is never silently routed to localai just because the chat
// config omitted an explicit endpoint. The result is normalized without a
// trailing slash because callers append "/chat/completions"; the public
// openrouter/ionos defaults are defined with a trailing slash and would
// otherwise produce a double slash (OpenRouter answers those with HTTP 404).
func resolveChatProviderEndpoint(backend, endpoint string) (string, error) {
	resolved, err := resolveProviderEndpoint(backend, endpoint)
	if err != nil {
		return "", err
	}
	resolved = strings.TrimRight(strings.TrimSpace(resolved), "/")
	if resolved == "" {
		resolved = "http://localai:8080"
	}
	return resolved, nil
}

func buildConfirmableActionFromToolCall(toolCall map[string]interface{}) map[string]interface{} {
	confirmation, ok := toolCall["confirmation"].(map[string]interface{})
	if !ok {
		return nil
	}

	targetToolName, _ := confirmation["target_tool_name"].(string)
	if targetToolName == "" {
		targetToolName, _ = toolCall["name"].(string)
	}

	actionID, _ := toolCall["id"].(string)
	action := map[string]interface{}{
		"action_id":             actionID,
		"source_tool_name":      toolCall["name"],
		"target_tool_name":      targetToolName,
		"status":                "pending",
		"requires_confirmation": true,
		"created_at":            time.Now().UTC().Format(time.RFC3339),
	}

	if suggested, exists := confirmation["suggested_inputs"]; exists {
		action["input"] = suggested
	}
	if toolInput, exists := confirmation["tool_input"]; exists {
		action["input"] = toolInput
	}
	if continueAfterExecute, ok := confirmation["continue_after_execute"].(bool); ok {
		action["continue_after_execute"] = continueAfterExecute
	}
	if title, ok := confirmation["title"].(string); ok && title != "" {
		action["title"] = title
	}
	if description, ok := confirmation["description"].(string); ok && description != "" {
		action["description"] = description
	}
	if confirmLabel, ok := confirmation["confirm_label"].(string); ok && confirmLabel != "" {
		action["confirm_label"] = confirmLabel
	}
	if dangerLevel, ok := confirmation["danger_level"].(string); ok && dangerLevel != "" {
		action["danger_level"] = dangerLevel
	}

	return action
}

func parseConfirmActionPayload(raw string) (map[string]interface{}, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, false
	}
	typeValue, _ := payload["type"].(string)
	if typeValue != "confirm-action" {
		return nil, false
	}
	return payload, true
}

func collectConfirmableActions(toolCalls []interface{}) []interface{} {
	actions := make([]interface{}, 0)
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]interface{})
		if !ok {
			continue
		}
		action := buildConfirmableActionFromToolCall(toolCall)
		if action != nil {
			actions = append(actions, action)
		}
	}
	return actions
}

// confirmableTurnPrompt builds the human-readable fallback text for a turn that
// paused on one or more confirmable actions. It is persisted instead of an
// empty assistant message so the turn does not render as a bare empty bubble
// for consumers that do not resolve the chat UI extension.
func confirmableTurnPrompt(actions []interface{}) string {
	names := make([]string, 0, len(actions))
	seen := map[string]struct{}{}
	for _, rawAction := range actions {
		action, ok := rawAction.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := action["target_tool_name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			name, _ = action["source_tool_name"].(string)
			name = strings.TrimSpace(name)
		}
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, "`"+name+"`")
	}
	if len(names) == 0 {
		return "Waiting for your confirmation to continue."
	}
	return fmt.Sprintf("Waiting for your confirmation to run %s.", strings.Join(names, ", "))
}

// runtimeSelectorPayloadType is the discriminator a tool result carries to be
// surfaced as a runtime-selector widget. The git integration's
// git_propose_interaction_runtime tool emits exactly this shape; if another
// integration starts emitting it, the frontend already understands it.
const runtimeSelectorPayloadType = "runtime-selector"

// parseRuntimeSelectorPayload detects a runtime-selector proposal returned as a
// tool result. It returns false for any other payload so unrelated tool results
// are left untouched.
func parseRuntimeSelectorPayload(raw string) (map[string]interface{}, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, false
	}
	typeValue, _ := payload["type"].(string)
	if typeValue != runtimeSelectorPayloadType {
		return nil, false
	}
	if choices, ok := payload["choices"].([]interface{}); !ok || len(choices) == 0 {
		return nil, false
	}
	return payload, true
}

// collectRuntimeSelectors gathers the runtime-selector payloads attached to the
// finished tool calls so they can be persisted alongside the message meta. The
// frontend renders them as the runtime choice widget.
func collectRuntimeSelectors(toolCalls []interface{}) []interface{} {
	selectors := make([]interface{}, 0)
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]interface{})
		if !ok {
			continue
		}
		selector, ok := toolCall["runtime_selector"].(map[string]interface{})
		if !ok {
			continue
		}
		// A message can carry more than one selector; give each a stable id so
		// the frontend can target a specific one. The LLM tool-call id is unique
		// within the message and stable across partial/final deliveries.
		if id, _ := selector["id"].(string); strings.TrimSpace(id) == "" {
			if toolCallID, _ := toolCall["id"].(string); strings.TrimSpace(toolCallID) != "" {
				selector["id"] = toolCallID
			}
		}
		selectors = append(selectors, selector)
	}
	return selectors
}

// trustedRuntimeSelfHost returns the base URL an in-process tool execution can
// use to call back into the backend API. It mirrors the confirmable-action
// executor's toolRuntimeSelfHost: the bot client normally carries the
// configured backend host, but a tool-backed bot reply must still be able to
// address the API when that host is unset, so fall back to the configured
// self host (HOST/PORT) instead of leaving api_host empty.
func trustedRuntimeSelfHost() string {
	host := strings.TrimSpace(os.Getenv("HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "1984"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

func trustedToolRuntime(aih *AIHandlerImpl, chatUUID string, senderUUID string) map[string]interface{} {
	runtime := map[string]interface{}{
		"chat_uuid":     strings.TrimSpace(chatUUID),
		"sender_uuid":   strings.TrimSpace(senderUUID),
		"bot_user_id":   aih.botContext.BotUser.ID,
		"bot_user_uuid": strings.TrimSpace(aih.botContext.BotUser.UUID),
	}
	// api_host is always populated so runtime-aware tools (eg the native
	// coder's hive-mind tools) never fail with "missing api_host/session_id in
	// tool init" even when the bot client has no host configured.
	runtime["api_host"] = trustedRuntimeSelfHost()
	if aih.botContext != nil && aih.botContext.Client != nil {
		if host := strings.TrimSpace(aih.botContext.Client.GetHost()); host != "" {
			runtime["api_host"] = host
		}
		runtime["session_id"] = strings.TrimSpace(aih.botContext.Client.GetSessionId())
	}
	return runtime
}

func mergeToolInitWithTrustedRuntime(initData interface{}, runtime map[string]interface{}) map[string]interface{} {
	base := map[string]interface{}{}
	if typed, ok := initData.(map[string]interface{}); ok {
		for k, v := range typed {
			base[k] = v
		}
	}
	if _, has := base["api_host"]; !has {
		if apiHost, ok := runtime["api_host"].(string); ok && strings.TrimSpace(apiHost) != "" {
			base["api_host"] = strings.TrimSpace(apiHost)
		}
	}
	if sessionID, ok := runtime["session_id"].(string); ok && strings.TrimSpace(sessionID) != "" {
		base["session_id"] = sessionID
	}
	base["_runtime"] = runtime
	return base
}

// AIHandlerImpl implements the AIHandler interface
type AIHandlerImpl struct {
	botContext *BotContext
}

// NewAIHandler creates a new AI handler
func NewAIHandler(botContext *BotContext) *AIHandlerImpl {
	return &AIHandlerImpl{
		botContext: botContext,
	}
}

// GenerateResponse generates an AI response for a message
func (aih *AIHandlerImpl) GenerateResponse(ctx context.Context, message wsapi.NewMessage) error {
	startTime := time.Now()
	var thinkingTime time.Duration
	var thinkingStart time.Time

	// 1 - first check if its a command or a plain text message
	if strings.HasPrefix(message.Content.Text, "/") {
		command := strings.Replace(message.Content.Text, "/", "", 1)
		return aih.ProcessCommand(ctx, command, message)
	}

	// Make the generation cancellable independently of the asynq task: the
	// chat interrupt endpoint can then abort an in-flight provider request and
	// tool loop through the chat-scoped registry (a stop must not depend on the
	// queue cancellation, which the parked-confirmation path already completed).
	generationCtx, cancelGeneration := context.WithCancel(ctx)
	defer cancelGeneration()
	releaseGeneration := TrackActiveChatGeneration(message.Content.ChatUUID, cancelGeneration)
	defer releaseGeneration()
	// A supersession marker that this turn did not consume (it completed
	// normally) must not linger and suppress a genuinely later user stop.
	defer chatstate.ClearChatSuperseded(message.Content.ChatUUID)
	ctx = generationCtx

	// Load the chat and its current configuration
	err, chat := aih.botContext.Client.GetChat(message.Content.ChatUUID)
	if err != nil {
		return err
	}

	// Extract configuration
	configMap := make(map[string]interface{})
	if chat.Config != nil {
		if m, ok := chat.Config.(map[string]interface{}); ok {
			configMap = m
		}
	}

	endpoint := mapGetOrDefault[string](configMap, "endpoint", "")
	backend := mapGetOrDefault[string](configMap, "backend", "deepinfra")
	model := mapGetOrDefault[string](configMap, "model", "meta-llama-3.1-8b-instruct")
	reasoning := mapGetOrDefault[bool](configMap, "reasoning", false)
	context := mapInt64OrDefault(configMap, "context", 10)
	toolCallMaxTotal := mapInt64OrDefault(configMap, "tool_call_max_total", int64(DefaultToolCallMaxTotal))
	toolCallMaxFailed := mapInt64OrDefault(configMap, "tool_call_max_failed", int64(DefaultToolCallMaxFailed))
	tools := mapGetOrDefault[[]string](configMap, "tools", []string{})
	toolInit := mapGetOrDefault[map[string]interface{}](configMap, "tool_init", map[string]interface{}{})
	dynamicTools := mapGetOrDefault[map[string]interface{}](configMap, "dynamic_tools", map[string]interface{}{})
	mcpTools := mapGetOrDefault[map[string]interface{}](configMap, "mcp_tools", map[string]interface{}{})
	systemPrompt := mapGetOrDefault[string](configMap, "system_prompt", "You are a helpful assistant.")
	// context_summary is a compacted summary of earlier turns persisted by the
	// native coder agent (msgmate_coder/coder_compact, ci#394). It is injected
	// as a leading system message so the dropped history survives in context.
	contextSummary := mapGetOrDefault[string](configMap, "context_summary", "")
	tags := mapGetOrDefault[[]string](configMap, "tags", []string{})
	samplingParams := samplingParamsFromConfig(configMap)
	providerRetryOptions := providerRetryOptionsFromConfig(configMap)
	trackUsage := mapGetOrDefault[bool](configMap, "track_usage", false)
	if providerRetryOptions.Enabled {
		providerRetryOptions.OnRetryAttempt = aih.makeProviderRetryReporter(message.Content.ChatUUID)
	}

	if toolCallMaxTotal < 1 {
		toolCallMaxTotal = int64(DefaultToolCallMaxTotal)
	}
	if toolCallMaxFailed < 1 {
		toolCallMaxFailed = int64(DefaultToolCallMaxFailed)
	}

	endpoint, err = resolveChatProviderEndpoint(backend, endpoint)
	if err != nil {
		return err
	}

	// Check for skip-core tag
	if aih.hasSkipCoreTag(tags) {
		log.Printf("Chat %s has skip-core tag, executing tools only", message.Content.ChatUUID)
		return aih.executeToolsOnly(ctx, message, tools, toolInit, dynamicTools, mcpTools)
	}

	// Load the past messages
	paginatedMessages, err := aih.loadContextMessages(message.Content.ChatUUID, context)
	if err != nil {
		return err
	}

	// Build OpenAI messages
	openAiMessages := aih.buildOpenAIMessages(paginatedMessages, message, systemPrompt, backend)
	openAiMessages = injectContextSummary(openAiMessages, contextSummary)

	// Setup tools
	toolsData, toolMap, interactionStartTools, interactionCompleteTools := aih.setupTools(message, tools, toolInit, dynamicTools, mcpTools)

	// Add run_callback_function to tools
	tools = append(tools, "run_callback_function")

	// Log the extracted interaction tools for debugging
	if len(interactionStartTools) > 0 {
		log.Printf("Found interaction_start tools: %v", interactionStartTools)
	}
	if len(interactionCompleteTools) > 0 {
		log.Printf("Found interaction_complete tools: %v", interactionCompleteTools)
	}

	// A turn triggered by a confirmable-action execution is a "post-confirmation
	// continuation". The triggering message is an event row carrying
	// event_type=confirmable_action_execute.
	postConfirmationContinuation := false
	if message.Content.MetaData != nil {
		if eventType, ok := (*message.Content.MetaData)["event_type"].(string); ok && eventType == "confirmable_action_execute" {
			postConfirmationContinuation = true
		}
	}

	// Stream chat completion
	chunks, usage, toolCalls, errs := streamChatCompletion(
		ctx,
		endpoint,
		model,
		backend,
		int(toolCallMaxTotal),
		int(toolCallMaxFailed),
		openAiMessages,
		toolsData,
		toolMap,
		aih.botContext.Client.GetApiKey(backend),
		interactionStartTools,
		interactionCompleteTools,
		GetGlobalMsgmateHandler(),
		samplingParams,
		providerRetryOptions,
		providerRequestMeta{
			ChatUUID:                     message.Content.ChatUUID,
			TrackUsage:                   trackUsage,
			PostConfirmationContinuation: postConfirmationContinuation,
		},
	)

	// Process the streaming response
	return aih.processStreamingResponse(ctx, message, chunks, usage, toolCalls, errs, startTime, thinkingTime, thinkingStart, reasoning, model, backend)
}

// ProcessCommand processes bot commands (like /pong, /loop)
func (aih *AIHandlerImpl) ProcessCommand(ctx context.Context, command string, message wsapi.NewMessage) error {
	if strings.HasPrefix(command, "pong") {
		aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
			Text: fmt.Sprintf("PONG '%s' ", command),
		})
		return nil
	} else if strings.HasPrefix(command, "loop") {
		var timeSlept float32 = 0.0
		for {
			aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text: fmt.Sprintf("LOOP '%f' ", timeSlept),
			})
			time.Sleep(1 * time.Second)
			timeSlept += 1
			if timeSlept > 10 {
				break
			}
		}
		return nil
	}
	return fmt.Errorf("unknown command '%s'", command)
}

// isNonConversationalMessage reports whether a chat row is bookkeeping that
// buildOpenAIMessages drops before sending history to the model (eg the event
// rows a confirmable-action approval creates). Such rows must not count against
// the context window.
func isNonConversationalMessage(msg client.ListedMessage) bool {
	if msg.DataType == "event" {
		return true
	}
	if msg.MetaData != nil {
		if eventType, ok := (*msg.MetaData)["event_type"].(string); ok && eventType == "confirmable_action_execute" {
			return true
		}
	}
	return false
}

// countConversationalMessages returns how many rows are real conversation turns.
func countConversationalMessages(rows []client.ListedMessage) int {
	count := 0
	for _, row := range rows {
		if !isNonConversationalMessage(row) {
			count++
		}
	}
	return count
}

// trimMessagesToContextWindow keeps the newest rows up to and including the
// `limit`-th conversational (non-event) message so event rows never consume the
// context budget. `rows` must be newest-first, matching the paginated message
// API ordering.
func trimMessagesToContextWindow(rows []client.ListedMessage, limit int) []client.ListedMessage {
	if limit < 1 {
		limit = 1
	}
	seen := 0
	for i, row := range rows {
		if isNonConversationalMessage(row) {
			continue
		}
		seen++
		if seen >= limit {
			return rows[:i+1]
		}
	}
	return rows
}

// loadContextMessages fetches the recent history fed to the model. The message
// API paginates raw rows, so a chat with many event messages (eg a multi-step
// confirmable-action flow) can exhaust a plain `limit` window with
// non-conversational rows and push the user's original task out of context,
// leaving the model to answer without it. We page backwards until we have
// `context` conversational messages (or the chat is exhausted), then trim to the
// same budget.
func (aih *AIHandlerImpl) loadContextMessages(chatUUID string, context int64) (*client.PaginatedMessages, error) {
	if context < 1 {
		context = 1
	}
	const maxContextPages = 8

	var (
		rows    []client.ListedMessage
		last    client.PaginatedMessages
		fetched bool
	)
	for page := int64(1); page <= maxContextPages; page++ {
		err, pageData := aih.botContext.Client.GetMessages(chatUUID, page, context)
		if err != nil {
			if !fetched {
				return nil, err
			}
			break
		}
		fetched = true
		last = pageData
		rows = append(rows, pageData.Rows...)

		if len(pageData.Rows) == 0 ||
			int(page) >= pageData.TotalPages ||
			countConversationalMessages(rows) >= int(context) {
			break
		}
	}

	last.Rows = trimMessagesToContextWindow(rows, int(context))
	return &last, nil
}

// buildOpenAIMessages builds the OpenAI messages array from chat history
func (aih *AIHandlerImpl) buildOpenAIMessages(paginatedMessages *client.PaginatedMessages, message wsapi.NewMessage, systemPrompt, backend string) []map[string]interface{} {
	openAiMessages := []map[string]interface{}{}
	currentMessageIncluded := false

	// A non-conversational lifecycle event (for example a confirmable-action
	// execution) is not a user turn. Its display text must not be injected as a
	// synthetic user message after the tool result, otherwise a mock/replay
	// provider that looks for the tool result before the next user turn re-issues
	// the confirmed tool call and loops. Detect it here so the current-message
	// append below can be skipped.
	triggerNonConversational := false
	if message.Content.DataType == "event" {
		triggerNonConversational = true
	}
	if message.Content.MetaData != nil {
		if eventType, ok := (*message.Content.MetaData)["event_type"].(string); ok &&
			eventType == "confirmable_action_execute" {
			triggerNonConversational = true
		}
	}

	// Add system prompt
	openAiMessages = append(openAiMessages, map[string]interface{}{"role": "system", "content": systemPrompt})

	// Process historical messages
	for i := len(paginatedMessages.Rows) - 1; i >= 0; i-- {
		msg := paginatedMessages.Rows[i]

		if msg.DataType == "event" {
			continue
		}
		if msg.MetaData != nil {
			if eventType, ok := (*msg.MetaData)["event_type"].(string); ok && eventType == "confirmable_action_execute" {
				continue
			}
		}

		// Check if message has attachments
		var attachments []interface{}
		if msg.MetaData != nil {
			if attData, ok := (*msg.MetaData)["attachments"]; ok {
				if attList, ok := attData.([]interface{}); ok {
					attachments = attList
				}
			}
		}

		if msg.SenderUUID == aih.botContext.Client.User.UUID {
			if msg.ToolCalls != nil && len(*msg.ToolCalls) > 0 {
				toolCallsPayload := make([]map[string]interface{}, 0, len(*msg.ToolCalls))
				for _, rawToolCall := range *msg.ToolCalls {
					toolCallMap, ok := rawToolCall.(map[string]interface{})
					if !ok {
						continue
					}
					toolCallID, _ := toolCallMap["id"].(string)
					toolName, _ := toolCallMap["name"].(string)
					if toolCallID == "" || toolName == "" {
						continue
					}

					argumentsText := "{}"
					switch typedArguments := toolCallMap["arguments"].(type) {
					case string:
						if strings.TrimSpace(typedArguments) != "" {
							argumentsText = typedArguments
						}
					default:
						if encodedArguments, err := json.Marshal(typedArguments); err == nil {
							argumentsText = string(encodedArguments)
						}
					}

					toolCallsPayload = append(toolCallsPayload, map[string]interface{}{
						"type": "function",
						"id":   toolCallID,
						"function": map[string]interface{}{
							"name":      toolName,
							"arguments": argumentsText,
						},
					})
				}

				if len(toolCallsPayload) > 0 {
					openAiMessages = append(openAiMessages, map[string]interface{}{
						"role":       "assistant",
						"content":    "",
						"tool_calls": toolCallsPayload,
					})

					for _, rawToolCall := range *msg.ToolCalls {
						toolCallMap, ok := rawToolCall.(map[string]interface{})
						if !ok {
							continue
						}
						toolCallID, _ := toolCallMap["id"].(string)
						if toolCallID == "" {
							continue
						}

						resultText := ""
						switch typedResult := toolCallMap["result"].(type) {
						case string:
							resultText = typedResult
						default:
							if encodedResult, err := json.Marshal(typedResult); err == nil {
								resultText = string(encodedResult)
							}
						}

						if strings.TrimSpace(resultText) == "" {
							continue
						}

						openAiMessages = append(openAiMessages, map[string]interface{}{
							"role":         "tool",
							"tool_call_id": toolCallID,
							"content":      resultText,
						})
					}
				}
			}

			if strings.TrimSpace(msg.Text) != "" {
				openAiMessages = append(openAiMessages, map[string]interface{}{"role": "assistant", "content": msg.Text})
			}
		} else {
			// Handle user messages with potential attachments
			contentArray := aih.processMessageAttachments(msg.Text, attachments, backend)
			openAiMessages = append(openAiMessages, map[string]interface{}{
				"role":    "user",
				"content": contentArray,
			})
		}
		if msg.Text == message.Content.Text {
			currentMessageIncluded = true
		}
	}

	// Add current message if not already included
	if !currentMessageIncluded && !triggerNonConversational {
		var attachments []interface{}
		if message.Content.Attachments != nil {
			// Convert FileAttachment slice to interface{} slice
			for _, att := range *message.Content.Attachments {
				attachments = append(attachments, map[string]interface{}{
					"file_id":   att.FileID,
					"mime_type": att.MimeType,
				})
			}
		}
		if strings.TrimSpace(message.Content.Text) != "" || len(attachments) > 0 {
			contentArray := aih.processCurrentMessageAttachments(message.Content.Text, attachments, backend)
			openAiMessages = append(openAiMessages, map[string]interface{}{
				"role":    "user",
				"content": contentArray,
			})
		}
	}

	return openAiMessages
}

// injectContextSummary inserts a compacted session summary as a leading system
// message, immediately after the primary system prompt, so earlier turns that
// have aged out of the context window survive in the model context. It is a
// no-op when summary is empty.
func injectContextSummary(messages []map[string]interface{}, summary string) []map[string]interface{} {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return messages
	}
	entry := map[string]interface{}{
		"role":    "system",
		"content": "Session summary (compacted context from earlier turns):\n" + summary,
	}
	if len(messages) == 0 {
		return []map[string]interface{}{entry}
	}
	if role, _ := messages[0]["role"].(string); role == "system" {
		out := make([]map[string]interface{}, 0, len(messages)+1)
		out = append(out, messages[0], entry)
		out = append(out, messages[1:]...)
		return out
	}
	return append([]map[string]interface{}{entry}, messages...)
}

// processMessageAttachments processes attachments for historical messages
func (aih *AIHandlerImpl) processMessageAttachments(text string, attachments []interface{}, backend string) interface{} {
	if len(attachments) > 0 {
		// Create content array with text and file references
		contentArray := []map[string]interface{}{}

		// Add text content if it exists
		if text != "" {
			contentArray = append(contentArray, map[string]interface{}{
				"type": "text",
				"text": text,
			})
		}

		// Process file attachments
		fileHandler := NewFileHandler(aih.botContext)
		processedAttachments, err := fileHandler.ProcessAttachments(attachments, backend)
		if err != nil {
			log.Printf("Error processing attachments: %v", err)
		} else {
			contentArray = append(contentArray, processedAttachments...)
		}

		return contentArray
	} else {
		return text
	}
}

// processCurrentMessageAttachments processes attachments for the current message
func (aih *AIHandlerImpl) processCurrentMessageAttachments(text string, attachments []interface{}, backend string) interface{} {
	if len(attachments) > 0 {
		// Create content array with text and file references
		contentArray := []map[string]interface{}{}

		// Add text content if it exists
		if text != "" {
			contentArray = append(contentArray, map[string]interface{}{
				"type": "text",
				"text": text,
			})
		}

		// Process file attachments
		fileHandler := NewFileHandler(aih.botContext)
		processedAttachments, err := fileHandler.ProcessAttachments(attachments, backend)
		if err != nil {
			log.Printf("Error processing attachments: %v", err)
		} else {
			contentArray = append(contentArray, processedAttachments...)
		}

		return contentArray
	} else {
		return text
	}
}

// setupTools sets up the tools for the AI response
func (aih *AIHandlerImpl) setupTools(message wsapi.NewMessage, tools []string, toolInit map[string]interface{}, dynamicTools map[string]interface{}, mcpTools map[string]interface{}) ([]interface{}, map[string]Tool, []string, []string) {
	var toolsData []interface{}
	toolMap := map[string]Tool{}
	var interactionStartTools []string
	var interactionCompleteTools []string

	EnsureExternalToolsRegistered()

	if len(tools) > 0 {
		trustedRuntime := trustedToolRuntime(aih, message.Content.ChatUUID, message.Content.SenderUUID)
		// Debug: Print all available tools in AllTools
		log.Printf("=== DEBUG: Available tools in AllTools ===")
		for i, tool := range AllTools {
			log.Printf("AllTools[%d]: %s (RequiresInit: %v)", i, tool.GetToolName(), tool.GetRequiresInit())
		}
		log.Printf("=== END DEBUG ===")

		for _, toolName := range tools {
			trustedRuntime = trustedToolRuntime(aih, message.Content.ChatUUID, message.Content.SenderUUID)
			actualToolName := toolName
			if strings.HasPrefix(toolName, "interaction_start:") {
				interactionStartTools = append(interactionStartTools, toolName)
				parts := strings.SplitN(toolName, ":", 2)
				if len(parts) == 2 {
					actualToolName = parts[1]
				}
			} else if strings.HasPrefix(toolName, "interaction_complete:") {
				interactionCompleteTools = append(interactionCompleteTools, toolName)
				parts := strings.SplitN(toolName, ":", 2)
				if len(parts) == 2 {
					actualToolName = parts[1]
				}
			}
			log.Printf("====> Processing tool registration for %s, with actual tool name %s", toolName, actualToolName)

			// Always instantiate a fresh tool per interaction. Mutating entries from AllTools
			// would share tool_init (e.g. task_pk) across concurrent interactions.
			tool, toolFound := NewToolByName(actualToolName)
			if actualToolName == "run_callback_function" {
				tool = NewRunCallbackFunctionTool()
				toolFound = true
			}
			if !toolFound || tool == nil {
				dynamicTool, dynamicFound, dynamicErr := NewDynamicRESTToolFromSnapshot(actualToolName, dynamicTools)
				if dynamicErr != nil {
					log.Printf("====> WARNING: Dynamic tool %s failed to load: %v", actualToolName, dynamicErr)
					continue
				}
				if dynamicFound && dynamicTool != nil {
					tool = dynamicTool
				} else {
					mcpTool, mcpFound, mcpErr := NewMCPToolFromSnapshot(actualToolName, mcpTools)
					if mcpErr != nil {
						log.Printf("====> WARNING: MCP tool %s failed to load: %v", actualToolName, mcpErr)
						continue
					}
					if !mcpFound || mcpTool == nil {
						log.Printf("====> WARNING: Tool %s NOT FOUND!", actualToolName)
						continue
					}
					tool = mcpTool
				}
			}
			log.Printf("====> Registered tool instance %s (RequiresInit: %v)", actualToolName, tool.GetRequiresInit())
			toolsData = append(toolsData, tool.ConstructTool())
			toolMap[toolName] = tool
			toolMap[tool.GetToolFunctionName()] = tool
			// Inject the trusted runtime into every tool the loop runs, not
			// only those that declare RequiresInit. Tool-backed bots (eg the
			// native coder) expose integration tools such as the hive-mind
			// lookup/observe/state/influence tools that forward their call to
			// the backend API and read api_host/session_id from init, but may
			// not ship an init schema. An explicit tool_init entry still wins
			// for api_host; the trusted session_id and _runtime are always
			// attached so runtime-aware tools never start with an empty scope.
			initData, ok := toolInit[toolName]
			if !ok {
				initData, ok = toolInit[actualToolName]
			}
			if ok {
				log.Printf("Setting init data for tool %s (toolName: %s)", tool.GetToolName(), toolName)
				tool.SetInitData(mergeToolInitWithTrustedRuntime(initData, trustedRuntime))
			} else {
				tool.SetInitData(mergeToolInitWithTrustedRuntime(map[string]interface{}{}, trustedRuntime))
			}
		}
	}

	return toolsData, toolMap, interactionStartTools, interactionCompleteTools
}

// processStreamingResponse processes the streaming response from the AI
func (aih *AIHandlerImpl) processStreamingResponse(ctx context.Context, message wsapi.NewMessage, chunks <-chan string, usage <-chan *TokenUsage, toolCalls <-chan ToolCall, errs <-chan error, startTime time.Time, thinkingTime time.Duration, thinkingStart time.Time, reasoning bool, model, backend string) error {
	var allToolCalls []interface{}
	var fullText, thoughtBuffer, currentThoughtStep strings.Builder
	var reasoningEntries []string
	var thinkingSteps []map[string]string
	var isThinking bool
	const thinkStartTag = "<think>"
	const thinkEndTag = "</think>"
	var currentBuffer strings.Builder
	// The turn timeline (ci#569) records the assistant's reasoning blocks in
	// order together with how many of them had already been emitted when each
	// tool call was dispatched, so the chat UI can render the thoughts between
	// the tool calls instead of showing only opaque call rows.
	timeline := newTurnTimeline()
	// thoughtDurations maps a reasoning block to the time the model spent
	// producing it, so a block extracted from the assistant text can still be
	// reported with its duration.
	thoughtDurations := map[string]int64{}
	partialSessionID := fmt.Sprintf("%s-%d", message.Content.ChatUUID, time.Now().UnixNano())
	var partialSeq int64
	nextPartialSeq := func() int64 {
		partialSeq++
		return partialSeq
	}
	streamSnapshot := newPartialMessagePersister(
		message.Content.ChatUUID,
		partialSessionID,
		aih.botContext.BotUser.ID,
	)
	persistStreamSnapshot := func() {
		entries := reasoningEntries
		if inProgress := strings.TrimSpace(currentThoughtStep.String()); inProgress != "" {
			entries = make([]string, len(reasoningEntries)+1)
			copy(entries, reasoningEntries)
			entries[len(reasoningEntries)] = inProgress
		}
		streamSnapshot.snapshot(partialSeq, fullText.String()+currentBuffer.String(), entries, allToolCalls)
	}
	flushStreamSnapshot := func() {
		entries := reasoningEntries
		if inProgress := strings.TrimSpace(currentThoughtStep.String()); inProgress != "" {
			entries = make([]string, len(reasoningEntries)+1)
			copy(entries, reasoningEntries)
			entries[len(reasoningEntries)] = inProgress
		}
		streamSnapshot.flushNow(partialSeq, fullText.String()+currentBuffer.String(), entries, allToolCalls)
	}
	disposeStreamSnapshot := func() {
		streamSnapshot.dispose()
	}
	thinkTagPattern := regexp.MustCompile(`(?is)<think>(.*?)</think>`)
	var tokenUsage *TokenUsage

	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.StartPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
		),
	)

	finalizePartial := func() {
		aih.botContext.WSHandler.MessageHandler.SendMessage(
			aih.botContext.WSHandler,
			message.Content.SenderUUID,
			aih.botContext.WSHandler.MessageHandler.EndPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
				partialSessionID,
			),
		)
	}

	appendThoughtEntries := func(entries []string) {
		for _, entry := range entries {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			alreadyExists := false
			for _, existing := range reasoningEntries {
				if existing == trimmed {
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				reasoningEntries = append(reasoningEntries, trimmed)
			}
		}
	}

	finalizeThinkingStep := func(duration time.Duration) {
		stepText := strings.TrimSpace(currentThoughtStep.String())
		if stepText == "" {
			currentThoughtStep.Reset()
			return
		}
		durationMs := duration.Milliseconds()
		if durationMs < 0 {
			durationMs = 0
		}
		thoughtDurations[stepText] = durationMs
		timeline.addThought(stepText, durationMs)
		appendThoughtEntries([]string{stepText})
		thinkingSteps = append(thinkingSteps, map[string]string{
			"text":     stepText,
			"duration": duration.Round(time.Millisecond).String(),
		})
		currentThoughtStep.Reset()
	}

	// finalizePendingTimelineThought records a reasoning block that is still
	// open (no closing think tag yet) before a tool call is registered, so the
	// thought that led to the call is not lost or rendered after it.
	finalizePendingTimelineThought := func() {
		stepText := strings.TrimSpace(currentThoughtStep.String())
		if stepText == "" {
			return
		}
		timeline.addThought(stepText, thoughtDurations[stepText])
		appendThoughtEntries([]string{stepText})
		currentThoughtStep.Reset()
	}

	// partialTimeline attaches the ordered reasoning timeline to a partial
	// message's meta data, including the block that is still streaming so the
	// UI can render it while it arrives (ci#569).
	partialTimeline := func(meta map[string]interface{}) map[string]interface{} {
		if meta == nil {
			meta = map[string]interface{}{}
		}
		if snapshot := timeline.snapshotWithStreaming(strings.TrimSpace(currentThoughtStep.String())); len(snapshot) > 0 {
			meta["timeline"] = snapshot
		}
		return meta
	}

	extractThinkSections := func(raw string) (string, []string) {
		if strings.TrimSpace(raw) == "" {
			return "", nil
		}

		matches := thinkTagPattern.FindAllStringSubmatchIndex(raw, -1)
		if len(matches) == 0 {
			if strings.Contains(raw, thinkEndTag) {
				parts := strings.Split(raw, thinkEndTag)
				if len(parts) > 1 {
					orphanThought := strings.ReplaceAll(parts[0], thinkStartTag, "")
					cleanTail := strings.Join(parts[1:], thinkEndTag)
					cleanTail = strings.ReplaceAll(cleanTail, thinkStartTag, "")
					cleanTail = strings.ReplaceAll(cleanTail, thinkEndTag, "")
					for strings.Contains(cleanTail, "\n\n\n") {
						cleanTail = strings.ReplaceAll(cleanTail, "\n\n\n", "\n\n")
					}
					return strings.TrimSpace(cleanTail), []string{orphanThought}
				}
			}
			clean := raw
			clean = strings.ReplaceAll(clean, thinkStartTag, "")
			clean = strings.ReplaceAll(clean, thinkEndTag, "")
			for strings.Contains(clean, "\n\n\n") {
				clean = strings.ReplaceAll(clean, "\n\n\n", "\n\n")
			}
			return strings.TrimSpace(clean), nil
		}

		thoughts := make([]string, 0, len(matches))
		var cleanBuilder strings.Builder
		lastIdx := 0
		for _, match := range matches {
			startIdx, endIdx := match[0], match[1]
			contentStart, contentEnd := match[2], match[3]
			if startIdx > lastIdx {
				cleanBuilder.WriteString(raw[lastIdx:startIdx])
			}
			thoughts = append(thoughts, raw[contentStart:contentEnd])
			lastIdx = endIdx
		}
		if lastIdx < len(raw) {
			cleanBuilder.WriteString(raw[lastIdx:])
		}

		clean := cleanBuilder.String()
		for strings.Contains(clean, "\n\n\n") {
			clean = strings.ReplaceAll(clean, "\n\n\n", "\n\n")
		}
		return strings.TrimSpace(clean), thoughts
	}

	// Helper function to send final message and cleanup
	sendFinalMessage := func(isCancelled bool, streamErr error) {
		// If we're still thinking when finishing, add the final thinking time
		totalTime := time.Since(startTime)

		finalizePartial()
		flushStreamSnapshot()
		disposeStreamSnapshot()

		text, extractedThoughts := extractThinkSections(fullText.String())
		appendThoughtEntries(extractedThoughts)
		if isCancelled {
			text += "\nI paused this response. Send another message when you want me to continue."
			if reasoning {
				reasoningEntries = append(reasoningEntries, "Response paused.")
			}
		}
		var streamFailure GenerationFailureClassification
		if streamErr != nil {
			text = strings.TrimSpace(text)
			streamFailure = ClassifyGenerationFailure(streamErr)
			reasoningEntry := ""
			switch streamFailure.Reason {
			case FailureReasonContextWindow:
				guidance := "The response exceeded the model's maximum context window. Try a shorter prompt, reduce large pasted content, or run commands that produce less output."
				reasoningEntry = "Response stopped because the model context window was exceeded."
				if text == "" {
					text = "I ran into an error while generating a reply. " + guidance + " Please try again in a moment."
				} else {
					text += "\n\nI ran into an error while finishing this reply. " + guidance
				}
			case FailureReasonProviderError:
				summary := streamFailure.ProviderSummary
				attempts := streamFailure.ProviderAttempts
				var guidance string
				if attempts > 1 {
					guidance = fmt.Sprintf(
						"The AI provider failed on all %d attempts (last error: %s). This error is coming from the AI provider itself, not from OpenChat - it may be rate limiting, an outage, or exhausted tokens/credits. You can retry in a moment or switch to a different model or provider.",
						attempts, summary)
					reasoningEntry = fmt.Sprintf("Response stopped by the AI provider: %s (attempts: %d).", summary, attempts)
				} else {
					guidance = fmt.Sprintf(
						"The AI provider returned an error (%s). This error is coming from the AI provider itself, not from OpenChat - it may be rate limiting, an outage, or exhausted tokens/credits.",
						summary,
					)
					reasoningEntry = fmt.Sprintf("Response stopped by the AI provider: %s.", summary)
				}
				if text == "" {
					text = fmt.Sprintf("I could not generate a reply: my AI provider request failed. %s Please try again in a moment or switch to another model.", guidance)
				} else {
					text += "\n\n" + guidance
				}
			case FailureReasonTransientNetwork:
				guidance := "This looks like a temporary network problem reaching the AI provider."
				reasoningEntry = "Response stopped due to a transient network error reaching the AI provider."
				if text == "" {
					text = "I can't reach my AI provider right now. " + guidance + " Please try again in a moment."
				} else {
					text += "\n\nI ran into a problem finishing this reply. " + guidance
				}
			case FailureReasonTimeout:
				// The turn ran longer than the queue allows. This is NOT a user
				// stop, so it must never render as "I paused this response"; the
				// work already done is kept and the user can continue (ci#642).
				guidance := "This turn ran longer than the server's time limit for a single reply and was stopped. The work already done (tool calls, files, commits) is kept - send another message to continue from here."
				reasoningEntry = "Response stopped because the turn exceeded the server's time limit."
				if text == "" {
					text = guidance
				} else {
					text += "\n\n" + guidance
				}
			case FailureReasonLeakedCompaction:
				// The model volunteered its own conversation-compaction
				// bookkeeping (<analysis>/<summary>) instead of an answer, twice
				// in a row; the transcript itself is never stored or shown
				// (ci#562). Everything the turn already did is kept.
				guidance := "The model lost track of the task and returned its internal work notes instead of an answer, so this reply was stopped. The work already done (tool calls, files, commits) is kept - send another message to continue from here."
				reasoningEntry = "Response stopped because the model returned its internal work notes instead of an answer."
				if text == "" {
					text = guidance
				} else {
					text += "\n\n" + guidance
				}
			default:
				detail := TruncateFailureDetail(streamErr.Error())
				reasoningEntry = fmt.Sprintf("Response stopped due to an internal error: %s.", detail)
				if text == "" {
					text = fmt.Sprintf("I hit an internal error while generating this reply: %s", detail)
				} else {
					text += fmt.Sprintf("\n\nI hit an internal error while finishing this reply: %s", detail)
				}
			}
			if reasoning {
				reasoningEntries = append(reasoningEntries, reasoningEntry)
			}
		}

		metadata := map[string]interface{}{
			"total_time": totalTime.Round(time.Millisecond).String(),
			"cancelled":  isCancelled,
			"finished":   true,
		}
		if model != "" {
			metadata["model"] = model
		}
		if backend != "" {
			metadata["backend"] = backend
		}
		if streamErr != nil {
			metadata["error"] = true
			metadata["failure_reason"] = streamFailure.Reason
			metadata["error_detail"] = streamErr.Error()
			if streamFailure.ProviderSummary != "" {
				metadata["provider_error"] = streamFailure.ProviderSummary
			}
			if code, body, attempts, isProviderErr := providerErrorInfo(streamErr); isProviderErr {
				metadata["error_code"] = code
				if summary, providerName := extractProviderErrorDetail(code, body); summary != "" {
					if _, exists := metadata["provider_error"]; !exists {
						metadata["provider_error"] = summary
					}
					if providerName != "" {
						metadata["provider_name"] = providerName
					}
				}
				if attempts > 1 {
					metadata["provider_retry_attempts"] = attempts
				}
			} else if streamFailure.ProviderAttempts > 1 {
				metadata["provider_retry_attempts"] = streamFailure.ProviderAttempts
			}
		}
		confirmableActions := collectConfirmableActions(allToolCalls)
		if len(confirmableActions) > 0 {
			metadata["confirmable_actions"] = confirmableActions
			// A turn that pauses on a confirmable action has no assistant text of
			// its own. Persisting it as an empty message leaves a bare bubble for
			// any consumer that does not resolve the chat UI extension (and its
			// inline confirm widget), so give it a short, human-readable prompt
			// instead.
			if strings.TrimSpace(text) == "" {
				text = confirmableTurnPrompt(confirmableActions)
			}
		}
		if runtimeSelectors := collectRuntimeSelectors(allToolCalls); len(runtimeSelectors) > 0 {
			metadata["runtime_selectors"] = runtimeSelectors
		}
		if tokenUsage != nil {
			metadata["token_usage"] = tokenUsage
		}
		if reasoning {
			metadata["thinking_time"] = thinkingTime.Round(time.Millisecond).String()
			if len(thinkingSteps) > 0 {
				metadata["thinking_steps"] = thinkingSteps
			}
		}
		// The ordered reasoning timeline is what the chat UI renders between the
		// tool calls (ci#569). A block that was still open when the turn ended is
		// recorded as a finished block (with an unknown duration).
		if pending := strings.TrimSpace(currentThoughtStep.String()); pending != "" {
			timeline.addThought(pending, thoughtDurations[pending])
			currentThoughtStep.Reset()
		}
		if snapshot := timeline.snapshot(); len(snapshot) > 0 {
			metadata["timeline"] = snapshot
		}

		if len(reasoningEntries) > 0 {
			aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text:      text,
				Reasoning: reasoningEntries,
				MetaData:  &metadata,
				ToolCalls: &allToolCalls,
			})
		} else {
			aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
				Text:      text,
				MetaData:  &metadata,
				ToolCalls: &allToolCalls,
			})
		}
	}

	currentThinkingDuration := func() time.Duration {
		if !isThinking {
			return thinkingTime
		}
		return thinkingTime + time.Since(thinkingStart)
	}

	sendRespondingChunk := func(chunk string) {
		if chunk == "" {
			return
		}
		fullText.WriteString(chunk)
		totalTime := time.Since(startTime)
		if reasoning {
			thinkingElapsed := currentThinkingDuration()
			seq := nextPartialSeq()
			meta := partialTimeline(map[string]interface{}{
				"thinking_time": thinkingElapsed.Round(time.Millisecond).String(),
				"total_time":    totalTime.Round(time.Millisecond).String(),
				"partial_phase": "responding",
			})
			aih.botContext.WSHandler.MessageHandler.SendMessage(
				aih.botContext.WSHandler,
				message.Content.SenderUUID,
				aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
					message.Content.ChatUUID,
					message.Content.SenderUUID,
					partialSessionID,
					seq,
					chunk,
					[]string{""},
					&meta,
					nil,
					nil,
				),
			)
			persistStreamSnapshot()
			return
		}

		seq := nextPartialSeq()
		meta := partialTimeline(map[string]interface{}{
			"total_time":    totalTime.Round(time.Millisecond).String(),
			"partial_phase": "responding",
		})
		aih.botContext.WSHandler.MessageHandler.SendMessage(
			aih.botContext.WSHandler,
			message.Content.SenderUUID,
			aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
				partialSessionID,
				seq,
				chunk,
				[]string{},
				&meta,
				nil,
				nil,
			),
		)
		persistStreamSnapshot()
	}

	sendThinkingChunk := func(chunk string) {
		if chunk == "" {
			return
		}
		thoughtBuffer.WriteString(chunk)
		currentThoughtStep.WriteString(chunk)
		totalTime := time.Since(startTime)
		thinkingElapsed := currentThinkingDuration()
		seq := nextPartialSeq()
		meta := partialTimeline(map[string]interface{}{
			"thinking_time": thinkingElapsed.Round(time.Millisecond).String(),
			"total_time":    totalTime.Round(time.Millisecond).String(),
			"partial_phase": "thinking",
		})
		aih.botContext.WSHandler.MessageHandler.SendMessage(
			aih.botContext.WSHandler,
			message.Content.SenderUUID,
			aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
				message.Content.ChatUUID,
				message.Content.SenderUUID,
				partialSessionID,
				seq,
				"",
				[]string{chunk},
				&meta,
				nil,
				nil,
			),
		)
		persistStreamSnapshot()
	}

	processChunk := func(chunk string) {
		if !reasoning {
			sendRespondingChunk(chunk)
			return
		}

		currentBuffer.WriteString(chunk)
		bufferStr := currentBuffer.String()

		for {
			if !isThinking {
				startIdx := strings.Index(bufferStr, thinkStartTag)
				if startIdx == -1 {
					keepSuffix := len(thinkStartTag) - 1
					if len(bufferStr) <= keepSuffix {
						break
					}
					sendRespondingChunk(bufferStr[:len(bufferStr)-keepSuffix])
					bufferStr = bufferStr[len(bufferStr)-keepSuffix:]
					break
				}

				if startIdx > 0 {
					sendRespondingChunk(bufferStr[:startIdx])
				}
				isThinking = true
				thinkingStart = time.Now()
				bufferStr = bufferStr[startIdx+len(thinkStartTag):]
				continue
			}

			endIdx := strings.Index(bufferStr, thinkEndTag)
			if endIdx == -1 {
				keepSuffix := len(thinkEndTag) - 1
				if len(bufferStr) <= keepSuffix {
					break
				}
				sendThinkingChunk(bufferStr[:len(bufferStr)-keepSuffix])
				bufferStr = bufferStr[len(bufferStr)-keepSuffix:]
				break
			}

			if endIdx > 0 {
				sendThinkingChunk(bufferStr[:endIdx])
			}
			isThinking = false
			stepDuration := time.Since(thinkingStart)
			thinkingTime += stepDuration
			finalizeThinkingStep(stepDuration)
			thinkingStart = time.Time{}
			bufferStr = bufferStr[endIdx+len(thinkEndTag):]
		}

		currentBuffer.Reset()
		currentBuffer.WriteString(bufferStr)
	}

	for {
		select {
		case <-ctx.Done():
			// Distinguish a genuine stop from the queue task's own deadline. A
			// user interrupt / a newer message cancels the generation context
			// (context.Canceled) and must still surface the pause marker, but a
			// task that simply exceeded its deadline (context.DeadlineExceeded)
			// is a timeout, not a user pause: a native coder turn routinely runs
			// long and used to be marked cancelled here even though it did real
			// work (ci#642).
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				log.Printf("Generation deadline exceeded. Stopping response for chat %s\n", message.Content.ChatUUID)
				sendFinalMessage(false, ctx.Err())
				return fmt.Errorf("%w: %v", ErrResponseAlreadySent, ctx.Err())
			}
			// A newer message into a chat with a live turn supersedes it. That
			// turn must NOT render as a user pause - the newer message carries
			// the conversation forward, and persisting "I paused this response"
			// here both misleads the user and feeds the auto-continue loop
			// (ci#642).
			if chatstate.ConsumeChatSuperseded(message.Content.ChatUUID) {
				log.Printf("Generation superseded by a newer message for chat %s\n", message.Content.ChatUUID)
				sendFinalMessage(false, nil)
				return nil
			}
			log.Printf("Cancellation received. Stopping response for chat %s\n", message.Content.ChatUUID)
			sendFinalMessage(true, nil)
			return ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
			} else {
				processChunk(chunk)
			}
		case usageInfo, ok := <-usage:
			if !ok {
				usage = nil
			} else {
				tokenUsage = usageInfo
			}
		case toolCall, ok := <-toolCalls:
			if !ok {
				toolCalls = nil
			} else {
				// Every call captures the narration the model emitted before it.
				// The message body keeps only the text that came after the
				// *final* call, so a turn that ran several tools would otherwise
				// render as a bare stack of call rows with no hint of what the
				// agent was doing or why (ci#569).
				capturedText, extractedThoughts := extractThinkSections(fullText.String())
				for _, thought := range extractedThoughts {
					trimmed := strings.TrimSpace(thought)
					if trimmed == "" {
						continue
					}
					timeline.addThought(trimmed, thoughtDurations[trimmed])
				}
				appendThoughtEntries(extractedThoughts)
				if narration := strings.TrimSpace(capturedText); narration != "" {
					timeline.addText(narration)
				}
				fullText.Reset()
				currentBuffer.Reset()
				fmt.Println("toolCall", toolCall.ToolName, toolCall.ToolInput)

				// A reasoning block that is still streaming when the call is
				// dispatched belongs *before* it in the timeline.
				finalizePendingTimelineThought()

				toolStatus := strings.TrimSpace(toolCall.Status)
				if toolStatus == "" {
					if strings.Contains(strings.ToLower(toolCall.Result), " failed with error:") {
						toolStatus = ToolCallStatusFailed
					} else if strings.TrimSpace(toolCall.Result) != "" {
						toolStatus = ToolCallStatusSucceeded
					} else {
						toolStatus = ToolCallStatusOngoing
					}
				}

				toolCallRepr := map[string]interface{}{
					"id":        toolCall.Id,
					"name":      toolCall.ToolName,
					"arguments": toolCall.ToolInput,
					"result":    toolCall.Result,
					"status":    toolStatus,
					// timeline_index is the number of reasoning blocks emitted
					// before this call was dispatched, so the chat UI can render
					// the thought the agent produced right before it (ci#569).
					"timeline_index": timeline.index(),
				}
				if strings.TrimSpace(toolCall.Error) != "" {
					toolCallRepr["error"] = toolCall.Error
				}
				// ci#688: a call that failed for an environmental reason is
				// budget-exempt and reports how many retries it needed, so a
				// saturated sandbox pool is visible in the transcript instead
				// of only in the backend log.
				if toolCall.Retries > 0 {
					toolCallRepr["retries"] = toolCall.Retries
				}
				if toolCall.BudgetExempt {
					toolCallRepr["budget_exempt"] = true
				}
				// Per-call timing (ci#569): started_at lets the UI run a live
				// timer while the call is ongoing, duration_ms renders the final
				// elapsed time once it completed. A completed call reports its
				// duration even when it was zero (a cached call is instant), so
				// the widget shows "0ms" instead of a fabricated elapsed time.
				for key, value := range toolCallTimingFields(toolStatus, toolCall.StartedAtUnixMs, toolCall.DurationMs) {
					toolCallRepr[key] = value
				}

				if tool, found := NewToolByName(toolCall.ToolName); found {
					// Only report a static confirmation requirement for a call
					// that actually paused. A code-defined resolver may have
					// auto-resolved the call (eg coder_ask in an autonomous
					// session), in which case its result is a normal succeeded
					// tool output and must not render as a pending confirmation.
					if toolStatus == ToolCallStatusPendingConfirmation && tool.GetRequiresConfirmation() {
						toolCallRepr["requires_confirmation"] = true
					}
					if confirmationMeta, ok := parseConfirmActionPayload(toolCall.Result); ok {
						toolCallRepr["requires_confirmation"] = true
						toolCallRepr["confirmation"] = confirmationMeta
					}
				}

				// Runtime-selector proposals are emitted by integration tools
				// (eg the git integration's git_propose_interaction_runtime).
				// They are surfaced to the frontend as a choice widget; unlike a
				// confirmable action they do not gate the current tool call.
				if selectorMeta, ok := parseRuntimeSelectorPayload(toolCall.Result); ok {
					toolCallRepr["runtime_selector"] = selectorMeta
				}

				updated := false
				for i, registeredToolCall := range allToolCalls {
					existing, ok := registeredToolCall.(map[string]interface{})
					if !ok {
						continue
					}
					if existing["id"] == toolCall.Id {
						for key, value := range toolCallRepr {
							// Keep the position the call was first dispatched at.
							if key == "timeline_index" {
								continue
							}
							existing[key] = value
						}
						allToolCalls[i] = existing
						updated = true
						break
					}
				}
				if !updated {
					allToolCalls = append(allToolCalls, toolCallRepr)
				}

				totalTime := time.Since(startTime)
				partialMeta := partialTimeline(map[string]interface{}{
					"total_time":       totalTime.Round(time.Millisecond).String(),
					"tool_call":        toolCall.ToolName,
					"tool_call_status": toolStatus,
					"partial_phase":    "tool_call",
				})
				if strings.TrimSpace(toolCall.Error) != "" {
					partialMeta["tool_call_error"] = toolCall.Error
				}
				confirmableActions := collectConfirmableActions(allToolCalls)
				if len(confirmableActions) > 0 {
					partialMeta["confirmable_actions"] = confirmableActions
				}
				if runtimeSelectors := collectRuntimeSelectors(allToolCalls); len(runtimeSelectors) > 0 {
					partialMeta["runtime_selectors"] = runtimeSelectors
				}
				seq := nextPartialSeq()
				aih.botContext.WSHandler.MessageHandler.SendMessage(
					aih.botContext.WSHandler,
					message.Content.SenderUUID,
					aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
						message.Content.ChatUUID,
						message.Content.SenderUUID,
						partialSessionID,
						seq,
						"",
						[]string{""},
						&partialMeta,
						&allToolCalls,
						nil,
					),
				)
				persistStreamSnapshot()
			}
		case err, ok := <-errs:
			if ok && err != nil {
				log.Printf("streamChatCompletion error: %v", err)
				sendFinalMessage(false, err)
				return fmt.Errorf("%w: %v", ErrResponseAlreadySent, err)
			}
			errs = nil
		}

		if chunks == nil && usage == nil && toolCalls == nil && errs == nil {
			break
		}
	}

	if reasoning {
		remaining := currentBuffer.String()
		if remaining != "" {
			if isThinking {
				sendThinkingChunk(remaining)
			} else {
				sendRespondingChunk(remaining)
			}
			currentBuffer.Reset()
		}
		if isThinking {
			stepDuration := time.Since(thinkingStart)
			thinkingTime += stepDuration
			finalizeThinkingStep(stepDuration)
			isThinking = false
			thinkingStart = time.Time{}
		}
	}

	sendFinalMessage(false, nil)
	return nil
}

// hasSkipCoreTag checks if the tags contain "skip-core"
func (aih *AIHandlerImpl) hasSkipCoreTag(tags []string) bool {
	for _, tag := range tags {
		if tag == "skip-core" {
			return true
		}
	}
	return false
}

// executeToolsOnly executes only the before and after tools without AI completion
func (aih *AIHandlerImpl) executeToolsOnly(ctx context.Context, message wsapi.NewMessage, tools []string, toolInit map[string]interface{}, dynamicTools map[string]interface{}, mcpTools map[string]interface{}) error {
	startTime := time.Now()
	partialSessionID := fmt.Sprintf("%s-skip-core-%d", message.Content.ChatUUID, time.Now().UnixNano())

	// Setup tools
	_, toolMap, interactionStartTools, interactionCompleteTools := aih.setupTools(message, tools, toolInit, dynamicTools, mcpTools)

	// Add run_callback_function to tools
	tools = append(tools, "run_callback_function")

	// Log the extracted interaction tools for debugging
	if len(interactionStartTools) > 0 {
		log.Printf("Found interaction_start tools: %v", interactionStartTools)
	}
	if len(interactionCompleteTools) > 0 {
		log.Printf("Found interaction_complete tools: %v", interactionCompleteTools)
	}

	// Send start message
	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.StartPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
		),
	)

	// Execute interaction_start tools
	for _, toolName := range interactionStartTools {
		if err := aih.executeTool(ctx, toolName, toolMap, message, partialSessionID); err != nil {
			log.Printf("Error executing interaction_start tool %s: %v", toolName, err)
		}
	}

	// Execute interaction_complete tools
	for _, toolName := range interactionCompleteTools {
		if err := aih.executeTool(ctx, toolName, toolMap, message, partialSessionID); err != nil {
			log.Printf("Error executing interaction_complete tool %s: %v", toolName, err)
		}
	}

	// Send end message
	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.EndPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
		),
	)

	// Send a simple acknowledgment message
	totalTime := time.Since(startTime)
	metadata := map[string]interface{}{
		"total_time": totalTime.Round(time.Millisecond).String(),
		"cancelled":  false,
		"finished":   true,
		"skip_core":  true,
	}

	aih.botContext.Client.SendChatMessage(message.Content.ChatUUID, client.SendMessage{
		Text:     "Tools executed (AI completion skipped due to skip-core tag)",
		MetaData: &metadata,
	})

	return nil
}

// executeTool executes a single tool
func (aih *AIHandlerImpl) executeTool(ctx context.Context, toolName string, toolMap map[string]Tool, message wsapi.NewMessage, partialSessionID string) error {
	// Find the tool in the tool map
	tool, exists := toolMap[toolName]
	if !exists {
		return fmt.Errorf("tool %s not found", toolName)
	}

	// Create a simple tool call structure
	toolCall := ToolCall{
		Id:        fmt.Sprintf("skip-core-%d", time.Now().UnixNano()),
		ToolName:  toolName,
		ToolInput: map[string]interface{}{},
		Result:    "",
	}

	// Execute the tool
	var (
		result string
		err    error
	)
	if contextTool, ok := tool.(ContextTool); ok {
		result, err = contextTool.RunToolContext(ctx, toolCall.ToolInput)
	} else {
		result, err = tool.RunTool(toolCall.ToolInput)
	}
	if err != nil {
		return fmt.Errorf("error executing tool %s: %w", toolName, err)
	}

	// Update the tool call with the result
	toolCall.Result = result

	// Send tool execution message
	totalTime := time.Since(time.Now())
	aih.botContext.WSHandler.MessageHandler.SendMessage(
		aih.botContext.WSHandler,
		message.Content.SenderUUID,
		aih.botContext.WSHandler.MessageHandler.NewPartialMessage(
			message.Content.ChatUUID,
			message.Content.SenderUUID,
			partialSessionID,
			0,
			"",
			[]string{""},
			&map[string]interface{}{
				"total_time":    totalTime.Round(time.Millisecond).String(),
				"tool_call":     toolName,
				"skip_core":     true,
				"partial_phase": "tool_call",
			},
			&[]interface{}{map[string]interface{}{
				"id":        toolCall.Id,
				"name":      toolCall.ToolName,
				"arguments": toolCall.ToolInput,
				"result":    toolCall.Result,
			}},
			nil,
		),
	)

	return nil
}

// AIHandlerFactory creates an AI handler with the given context
func AIHandlerFactory(botContext *BotContext) AIHandler {
	return NewAIHandler(botContext)
}
