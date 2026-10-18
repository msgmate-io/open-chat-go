package msgmate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"backend/database"
	"backend/nativecoder"
)

// Terminal reasons a native coder turn can end with. They are recorded on the
// turn outcome and surfaced as message metadata, so a turn that ends after many
// tool calls explains *why* it ended instead of finishing on an empty message
// (ci#709).
const (
	TurnEndedCompleted         = "completed"
	TurnEndedToolCallCap       = "tool_call_cap_reached"
	TurnEndedFailedToolCallCap = "failed_tool_call_cap_reached"
	TurnEndedProviderError     = "provider_error"
	TurnEndedCancelled         = "cancelled"
	TurnEndedNoUsableCall      = "no_usable_tool_call"
	TurnEndedConfirmablePause  = "confirmable_pause"
)

// TurnOutcome records how a native coder turn ended and what its tool-call
// budget looked like when it did. The tool loop settles it before it closes its
// channels, so the reader observes a fully written value once the stream is
// drained.
type TurnOutcome struct {
	// Reason is one of the TurnEnded* values.
	Reason string
	// ToolCalls / FailedToolCalls are the consumed counters of the turn.
	ToolCalls       int
	FailedToolCalls int
	// TransientFailures / SandboxCapacityFailures are the budget-exempt failures
	// (ci#688, ci#691).
	TransientFailures       int
	SandboxCapacityFailures int
	// Budget is the resolved per-chat tool-call budget of the turn.
	Budget nativecoder.ToolCallBudget
}

// reason returns the recorded reason, or the completed default for a nil outcome.
func (o *TurnOutcome) reason() string {
	if o == nil || strings.TrimSpace(o.Reason) == "" {
		return TurnEndedCompleted
	}
	return o.Reason
}

// usage renders the consumed/remaining counters of the turn as structured
// state, so the caps no longer live only inside an error string.
func (o *TurnOutcome) usage() nativecoder.ToolCallBudgetUsage {
	if o == nil {
		return nativecoder.ToolCallBudget{}.Usage(0, 0, 0, 0)
	}
	return o.Budget.Usage(o.ToolCalls, o.FailedToolCalls, o.TransientFailures, o.SandboxCapacityFailures)
}

// turnBudgetFromMeta returns the budget the tool loop enforces: the one the
// caller resolved for the chat, or the explicit caps it was handed (a direct
// caller, e.g. a unit test).
func turnBudgetFromMeta(meta providerRequestMeta, maxTotal, maxFailed int) nativecoder.ToolCallBudget {
	if meta.Budget != nil {
		return *meta.Budget
	}
	return nativecoder.ToolCallBudget{
		MaxTotal:  maxTotal,
		MaxFailed: maxFailed,
		Source:    nativecoder.ToolCallBudgetSourceDefault,
	}
}

// ResolveTurnToolCallBudget resolves the tool-call budget of a native coder
// turn: the chat's own config and snapshot, raised by the bot runtime's
// *current* default shared config, so a cap raise on the bot reaches chats that
// already exist (ci#709).
func ResolveTurnToolCallBudget(botUserID uint, chatConfig map[string]interface{}) nativecoder.ToolCallBudget {
	return nativecoder.ResolveToolCallBudget(chatConfig, botRuntimeSharedConfig(botUserID))
}

// botRuntimeSharedConfig loads the active bot runtime's default shared config
// (nil when unknown), which is the source of a per-bot cap raise.
func botRuntimeSharedConfig(botUserID uint) map[string]interface{} {
	db := database.GetGlobalDB()
	if db == nil || botUserID == 0 {
		return nil
	}
	var runtime database.BotRuntimeConfig
	if err := db.Where("bot_user_id = ? AND is_active = ?", botUserID, true).Order("id desc").First(&runtime).Error; err != nil {
		return nil
	}
	if len(runtime.DefaultSharedConfig) == 0 {
		return nil
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(runtime.DefaultSharedConfig, &config); err != nil {
		return nil
	}
	return config
}

// ResolveTerminalAssistantText guarantees that a finished turn persists a
// non-empty, human-readable assistant message (ci#709).
//
// The native coder tool loop can end a turn in several ways — the model stops
// asking for tools, the per-chat tool-call cap is reached, a provider request
// fails, the user stops the turn, or a round surfaces no usable tool call (the
// loop guard). Every one of those must explain what happened and what to do
// next; a chat must never end with tool calls but no text, which is how a
// finished-looking turn silently lost its work.
//
// text is the assistant text the turn actually produced (may be empty),
// toolCalls the number of tool calls the turn ran, cancelled whether the turn
// was stopped, streamErr the terminal error (nil for a clean end) and outcome
// the recorded terminal reason/counters.
func ResolveTerminalAssistantText(text string, toolCalls int, cancelled bool, streamErr error, outcome *TurnOutcome) string {
	trimmed := strings.TrimSpace(text)
	// A stopped turn keeps whatever it already said and only adds the pause
	// marker, so the user always sees why the answer ends.
	if cancelled || errors.Is(streamErr, context.Canceled) {
		pause := "I paused this response. Send another message when you want me to continue."
		if trimmed != "" {
			if strings.Contains(trimmed, pause) {
				return trimmed
			}
			return trimmed + "\n" + pause
		}
		if toolCalls > 0 {
			return fmt.Sprintf(
				"I paused this turn after %d tool call(s). The work already done (tool calls, files, commits) is kept - send another message to continue from here.",
				toolCalls,
			)
		}
		return pause
	}
	if trimmed != "" {
		return trimmed
	}
	guidance := terminalTextGuidance(streamErr, outcome)
	if toolCalls > 0 {
		// The turn demonstrably did work: say so, and how to keep going.
		return fmt.Sprintf(
			"This turn ran %d tool call(s) but ended without a final summary. %s",
			toolCalls, guidance,
		)
	}
	return "I could not produce a reply for this message. " + guidance
}

// terminalTextGuidance returns the "what happened / what to do next" sentence
// for a terminal turn, from the terminal error and the recorded reason.
func terminalTextGuidance(streamErr error, outcome *TurnOutcome) string {
	var capErr *toolCallCapError
	if errors.As(streamErr, &capErr) {
		return capErr.guidance()
	}
	if errors.Is(streamErr, errLeakedCompactionTranscript) {
		return "The model lost track of the task and returned its internal work notes instead of an answer, so the reply was stopped. The work already done (tool calls, files, commits) is kept - send another message to continue from here."
	}
	if streamErr != nil {
		classification := ClassifyGenerationFailure(streamErr)
		switch classification.Reason {
		case FailureReasonContextWindow:
			return "The response exceeded the model's maximum context window. Try a shorter prompt, reduce large pasted content, or run commands that produce less output. The work already done is kept."
		case FailureReasonProviderError:
			return fmt.Sprintf("The AI provider failed the request (%s). This error comes from the AI provider, not from OpenChat - retry in a moment or switch model/provider. The work already done is kept.", classification.ProviderSummary)
		case FailureReasonTransientNetwork:
			return "I could not reach my AI provider right now. Please try again in a moment. The work already done is kept."
		case FailureReasonTimeout:
			return "This turn ran longer than the server's time limit for a single reply and was stopped. The work already done (tool calls, files, commits) is kept - send another message to continue from here."
		default:
			return fmt.Sprintf("It ended on an internal error (%s). The work already done is kept - send another message to continue from here.", TruncateFailureDetail(streamErr.Error()))
		}
	}

	switch outcome.reason() {
	case TurnEndedToolCallCap, TurnEndedFailedToolCallCap:
		return "It stopped because this chat's tool-call budget was exhausted. The work already done (tool calls, files, commits) is kept - send another message to continue from here."
	case TurnEndedNoUsableCall:
		return "The model stopped returning usable tool calls, so the turn ended here. The work already done (tool calls, files, commits) is kept - send another message to continue from here."
	case TurnEndedCancelled:
		return "It was stopped before it produced a final answer. The work already done is kept - send another message to continue from here."
	case TurnEndedConfirmablePause:
		return "It is waiting for you to confirm an action before it continues."
	default:
		return "The model returned no text for this turn. The work already done is kept - send another message to continue from here."
	}
}

// toolCallCapError is the typed terminal error of a turn that hit its per-chat
// tool-call budget. It keeps the historic error wording (other components match
// on it) and adds the structured counters and human-readable guidance, so the
// chat can explain the stop instead of only reporting a raw error string.
type toolCallCapError struct {
	kind string
	// max is the cap that was reached.
	max int
	// consumed counters of the turn.
	consumedTotal  int
	consumedFailed int
}

// Which cap a turn hit.
const (
	toolCallCapErrorKindTotal  = "total"
	toolCallCapErrorKindFailed = "failed"
)

func (e *toolCallCapError) Error() string {
	if e == nil {
		return "exceeded maximum number of tool calls"
	}
	if e.kind == toolCallCapErrorKindFailed {
		return fmt.Sprintf(
			"exceeded maximum number of failed tool calls (%d); consumed %d failed tool call(s) of a per-chat budget of %d",
			e.max, e.consumedFailed, e.max,
		)
	}
	return fmt.Sprintf(
		"exceeded maximum number of tool calls (%d); consumed %d tool call(s) of a per-chat budget of %d",
		e.max, e.consumedTotal, e.max,
	)
}

// guidance returns the user-facing explanation of a reached cap.
func (e *toolCallCapError) guidance() string {
	if e == nil {
		return "It stopped because this chat's tool-call budget was exhausted. The work already done is kept - send another message to continue from here."
	}
	if e.kind == toolCallCapErrorKindFailed {
		return fmt.Sprintf(
			"It stopped after %d failed tool call(s), which is this chat's failed-tool budget (%d). Retry the failed step with different arguments, or send another message to continue from here - the work already done is kept.",
			e.consumedFailed, e.max,
		)
	}
	return fmt.Sprintf(
		"It stopped after %d tool calls, which is this chat's tool-call budget (%d). The work already done (tool calls, files, commits) is kept - send another message to continue from here.",
		e.consumedTotal, e.max,
	)
}

// newToolCallCapError builds the terminal error of a reached tool-call cap.
func newToolCallCapError(kind string, max, consumedTotal, consumedFailed int) *toolCallCapError {
	return &toolCallCapError{
		kind:           kind,
		max:            max,
		consumedTotal:  consumedTotal,
		consumedFailed: consumedFailed,
	}
}
