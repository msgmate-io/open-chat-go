package msgmate

import (
	"context"
	"errors"

	wsapi "backend/api/websocket"
)

// This file implements the core side of the HostVersion 5 Agent runtime: a
// compiled-in integration (the coding integrations) hands the core a turn spec
// (its own system prompt, tool set, tool-init and turn hooks) and the core runs
// the same generic tool loop it runs for every native bot, keeping ownership of
// provider streaming, persistence, streaming frames, confirmations, compaction
// and the generation registry.
//
// The integration never imports the core for this: the core adapter in
// backend/integrationhost bridges integrationinterface.AgentRequest onto
// RunAgentTurn, carrying the request's BotContext and incoming message in the
// context the queue task already established.

// errNoAgentTurnContext is returned when an integration asks the Agent runtime
// to run a turn outside a bot-reply request (there is no BotContext to run it
// against). It is a programming error, not a transient failure.
var errNoAgentTurnContext = errors.New("agent runtime: no bot-reply turn context")

// AgentTurnSpec is the integration-owned specification of one agent turn.
type AgentTurnSpec struct {
	// SystemPrompt overrides the system prompt resolved from the chat config.
	SystemPrompt string
	// Tools overrides the tool-name set resolved from the chat config.
	Tools []string
	// ToolInit seeds per-tool init (tool_init) for the turn.
	ToolInit map[string]map[string]interface{}
	// Hooks are the integration's turn hooks (all optional).
	Hooks AgentTurnHooks
}

// AgentInitContext is the read-only input to the PreCallInit hook.
type AgentInitContext struct {
	Tools  []string
	Init   map[string]map[string]interface{}
	Tags   []string
	Config map[string]interface{}
}

// AgentTurnHooks mirrors integrationinterface.AgentHooks with core-local types.
// A nil field means "keep the core default behaviour".
type AgentTurnHooks struct {
	PreCallInit         func(ctx AgentInitContext) map[string]map[string]interface{}
	NormalizeToolCall   func(name, arguments string) string
	ToolFailureGuidance func(name, result string) string
	ToolBudgetExempt    func(name string) bool
	ToolSchemaExtend    func(name string, schema map[string]interface{})
}

// AgentTurnResult is the outcome of one agent turn.
type AgentTurnResult struct {
	MessageUUID string
	Text        string
	Finished    bool
}

type agentTurnContextKey struct{}

type agentTurnContext struct {
	botContext *BotContext
	message    wsapi.NewMessage
}

// WithAgentTurnContext records the bot-reply turn (its BotContext and incoming
// message) on the context so an integration-owned chat backend can later run the
// generic loop for the same turn via RunAgentTurn. It is installed by the core
// adapter around every external chat-backend invocation.
func WithAgentTurnContext(ctx context.Context, botContext *BotContext, message wsapi.NewMessage) context.Context {
	return context.WithValue(ctx, agentTurnContextKey{}, agentTurnContext{botContext: botContext, message: message})
}

func agentTurnFromContext(ctx context.Context) (agentTurnContext, bool) {
	turn, ok := ctx.Value(agentTurnContextKey{}).(agentTurnContext)
	return turn, ok && turn.botContext != nil
}

type agentTurnSpecKey struct{}

func withAgentTurnSpec(ctx context.Context, spec AgentTurnSpec) context.Context {
	return context.WithValue(ctx, agentTurnSpecKey{}, spec)
}

func agentTurnSpecFromContext(ctx context.Context) (AgentTurnSpec, bool) {
	spec, ok := ctx.Value(agentTurnSpecKey{}).(AgentTurnSpec)
	return spec, ok
}

func agentTurnHooksFromContext(ctx context.Context) (AgentTurnHooks, bool) {
	spec, ok := agentTurnSpecFromContext(ctx)
	if !ok {
		return AgentTurnHooks{}, false
	}
	return spec.Hooks, true
}

// RunAgentTurn runs the generic tool loop for an integration-owned agent turn.
// The context must carry the current bot-reply turn (WithAgentTurnContext); the
// core adapter installs it for every external chat-backend invocation.
func RunAgentTurn(ctx context.Context, spec AgentTurnSpec) (AgentTurnResult, error) {
	turn, ok := agentTurnFromContext(ctx)
	if !ok {
		return AgentTurnResult{}, errNoAgentTurnContext
	}
	ctx = withAgentTurnSpec(ctx, spec)
	handler := NewAIHandler(turn.botContext)
	if err := handler.GenerateResponse(ctx, turn.message); err != nil {
		return AgentTurnResult{}, err
	}
	return AgentTurnResult{Finished: true}, nil
}

// ---------------------------------------------------------------------------
// Hook accessors used by the shared tool loop.
// ---------------------------------------------------------------------------

// agentNormalizeToolCall applies the integration's NormalizeToolCall hook to a
// raw tool-call argument JSON string, or returns the original arguments when no
// hook is installed or the hook declines the rewrite.
func agentNormalizeToolCall(ctx context.Context, name, arguments string) string {
	hooks, ok := agentTurnHooksFromContext(ctx)
	if !ok || hooks.NormalizeToolCall == nil {
		return arguments
	}
	if normalized := hooks.NormalizeToolCall(name, arguments); normalized != "" {
		return normalized
	}
	return arguments
}

// agentToolFailureGuidance applies the integration's ToolFailureGuidance hook to
// a failed tool result, or returns the original result when no hook is installed
// or the hook declines.
func agentToolFailureGuidance(ctx context.Context, name, result string) string {
	hooks, ok := agentTurnHooksFromContext(ctx)
	if !ok || hooks.ToolFailureGuidance == nil {
		return result
	}
	if framed := hooks.ToolFailureGuidance(name, result); framed != "" {
		return framed
	}
	return result
}

// agentToolBudgetExempt reports whether the integration exempts a tool from the
// per-turn tool-call budget. It ORs with the core's built-in exemptions so a
// build without the integration keeps the current behaviour.
func agentToolBudgetExempt(ctx context.Context, name string) bool {
	if toolCallBudgetExempt(name) {
		return true
	}
	hooks, ok := agentTurnHooksFromContext(ctx)
	if !ok || hooks.ToolBudgetExempt == nil {
		return false
	}
	return hooks.ToolBudgetExempt(name)
}

// agentExtendToolSchema applies the integration's ToolSchemaExtend hook to a
// tool schema before it is sent to the provider.
func agentExtendToolSchema(ctx context.Context, name string, schema map[string]interface{}) map[string]interface{} {
	hooks, ok := agentTurnHooksFromContext(ctx)
	if !ok || hooks.ToolSchemaExtend == nil || schema == nil {
		return schema
	}
	hooks.ToolSchemaExtend(name, schema)
	return schema
}

// agentRunPreCallInit applies the integration's PreCallInit hook and returns the
// tool-init additions it seeded.
func agentRunPreCallInit(ctx context.Context, in AgentInitContext) map[string]map[string]interface{} {
	hooks, ok := agentTurnHooksFromContext(ctx)
	if !ok || hooks.PreCallInit == nil {
		return nil
	}
	return hooks.PreCallInit(in)
}

// mergeAgentToolInit folds an agent turn's tool-init into the chat config's
// tool_init: the spec's explicit ToolInit wins, then the PreCallInit hook fills
// any key still absent. A turn with nothing to add returns the config map
// unchanged.
func mergeAgentToolInit(ctx context.Context, current map[string]interface{}, spec AgentTurnSpec, tags []string, config map[string]interface{}) map[string]interface{} {
	typed := map[string]map[string]interface{}{}
	for key, raw := range current {
		if entry, ok := raw.(map[string]interface{}); ok {
			typed[key] = entry
		}
	}
	for key, payload := range spec.ToolInit {
		if len(payload) > 0 {
			typed[key] = payload
		}
	}
	if seeded := agentRunPreCallInit(ctx, AgentInitContext{Tools: spec.Tools, Init: typed, Tags: tags, Config: config}); len(seeded) > 0 {
		for key, payload := range seeded {
			if len(payload) == 0 {
				continue
			}
			if _, exists := typed[key]; exists {
				continue
			}
			typed[key] = payload
		}
	}
	if len(typed) == 0 {
		return current
	}
	out := make(map[string]interface{}, len(current)+len(typed))
	for key, value := range current {
		out[key] = value
	}
	for key, payload := range typed {
		out[key] = payload
	}
	return out
}
