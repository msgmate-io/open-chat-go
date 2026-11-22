package msgmate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"backend/database"
	"backend/toolbudget"
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
	// TurnEndedEmptyFinalText is recorded when a turn ran tool calls but
	// settled without any final assistant text, even after the single
	// synthetic summary nudge (ci#1065).
	TurnEndedEmptyFinalText = "empty_final_text"
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
	// BudgetExemptCalls counts the calls of the turn that were bookkeeping
	// rather than work (`coder_schedule`, ci#724) and therefore did not consume
	// the total tool-call budget.
	BudgetExemptCalls int
	// Budget is the resolved per-chat tool-call budget of the turn.
	Budget toolbudget.ToolCallBudget
	// Context is the context/token usage report of the last request the turn
	// sent (ci#680), so the reader can surface it as `context_budget` metadata
	// and the `coder_context` tool can answer with the turn's real numbers.
	Context ContextUsageReport
	// EmptyFinalStreak is the interaction state of ci#1065: how many turns of
	// the interaction in a row ended without a final summary, including this
	// one. AutoContinue reads it to report a streak instead of retrying.
	EmptyFinalStreak int
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
func (o *TurnOutcome) usage() toolbudget.ToolCallBudgetUsage {
	if o == nil {
		return toolbudget.ToolCallBudget{}.Usage(0, 0, 0, 0, 0)
	}
	return o.Budget.Usage(o.ToolCalls, o.FailedToolCalls, o.TransientFailures, o.SandboxCapacityFailures, o.BudgetExemptCalls)
}

// turnBudgetFromMeta returns the budget the tool loop enforces: the one the
// caller resolved for the chat, or the explicit caps it was handed (a direct
// caller, e.g. a unit test).
func turnBudgetFromMeta(meta providerRequestMeta, maxTotal, maxFailed int) toolbudget.ToolCallBudget {
	if meta.Budget != nil {
		return *meta.Budget
	}
	return toolbudget.ToolCallBudget{
		MaxTotal:  maxTotal,
		MaxFailed: maxFailed,
		Source:    toolbudget.ToolCallBudgetSourceDefault,
	}
}

// ResolveTurnToolCallBudget resolves the tool-call budget of a native coder
// turn: the chat's own config and snapshot, raised by the bot runtime's
// *current* default shared config, so a cap raise on the bot reaches chats that
// already exist (ci#709).
func ResolveTurnToolCallBudget(botUserID uint, chatConfig map[string]interface{}) toolbudget.ToolCallBudget {
	return toolbudget.ResolveToolCallBudget(chatConfig, botRuntimeSharedConfig(botUserID))
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
	const pauseMarker = "I paused this response. Send another message when you want me to continue."
	// A stopped turn keeps whatever it already said and only adds the pause
	// marker, so the user always sees why the answer ends.
	if cancelled || errors.Is(streamErr, context.Canceled) {
		// The caller may already have appended the bare pause marker; that is
		// not an answer, so a turn that did work still gets the richer summary.
		if trimmed != "" && trimmed != pauseMarker && !strings.HasSuffix(trimmed, "\n"+pauseMarker) {
			if strings.Contains(trimmed, pauseMarker) {
				return trimmed
			}
			return trimmed + "\n" + pauseMarker
		}
		if toolCalls > 0 {
			return fmt.Sprintf(
				"I paused this turn after %d tool call(s). The work already done (tool calls, files, commits) is kept - send another message to continue from here.",
				toolCalls,
			)
		}
		return pauseMarker
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
	// A reached tool-call cap is a capacity stop, not a model failure: it must
	// be explained as such even when the turn itself recorded no tool calls
	// (the budget is per chat, so an earlier turn may already have spent it).
	// Falling through would produce "could not produce a reply" and hide the
	// actual reason and the way forward.
	var capErr *toolCallCapError
	if errors.As(streamErr, &capErr) {
		return guidance
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
		case FailureReasonProviderBackpressure:
			// ci#909: the per-key provider admission slot cap was exhausted and
			// no slot freed up within the queue timeout, so the request never
			// reached the provider. This is OpenChat's own concurrency
			// back-pressure, never a provider fault: the user (or pipeline)
			// must lower concurrent agents, not switch model/provider.
			return "Too many AI requests are queued for this provider key right now, so this turn was stopped before reaching the AI provider. Please lower the number of concurrent requests (or wait a moment) and try again. The work already done is kept."
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
	case TurnEndedEmptyFinalText:
		return "The turn ended after tool calls without a final summary."
	default:
		return "The model returned no text for this turn. The work already done is kept - send another message to continue from here."
	}
}

// applyTerminalFailureMetadata records a failed terminal turn on the message
// metadata.
//
// ci#1160: a turn stopped by this chat's tool-call budget is a capacity stop the
// server performed by design - the cap exists to protect the chat and the
// sandbox from a runaway loop, and the turn ends with guidance telling the user
// the work done so far is kept. It must therefore still be recorded (its
// failure_reason is what makes a cap stop inspectable), but it must not be
// flagged as an error: `error: true` is what the chat UI and the ci#622
// native-coder agent self-check read as "the server broke", which classified a
// healthy budget stop as an internal defect.
//
// Both terminal outcomes keep turn_ended populated: it is written here from the
// recorded outcome (and again later in the handler), so a cap stop stays fully
// inspectable without being reported as an error.
func applyTerminalFailureMetadata(metadata map[string]interface{}, streamErr error, classification GenerationFailureClassification, outcome *TurnOutcome) {
	if metadata == nil {
		return
	}
	if streamErr == nil {
		return
	}
	metadata["failure_reason"] = classification.Reason
	if failureReasonExemptFromErrorFlag(classification.Reason) {
		// Capacity stop, not a fault: keep it out of the error surface.
		metadata["turn_ended"] = outcome.reason()
		return
	}
	metadata["error"] = true
	metadata["error_detail"] = streamErr.Error()
}

// failureReasonExemptFromErrorFlag reports whether a terminal failure reason
// describes a capacity/back-pressure stop the server performed by design rather
// than a fault, and therefore must not be flagged with `error: true`.
//
// ci#1179 (follow-up to ci#1160): provider backpressure joins the tool-call
// budget here. Back-pressure means the request never reached the provider
// because OpenChat's own admission cap was exhausted, and ci#1179 schedules a
// bounded recovery continuation for it. Flagging it as an error is what made
// the finished message resolve StateFailed (see chats.InteractionStatus), which
// in turn hid the recovery continuation from the user and made a healthy
// self-inflicted capacity stop look like a defect.
func failureReasonExemptFromErrorFlag(reason string) bool {
	switch reason {
	case FailureReasonToolCallBudget, FailureReasonProviderBackpressure:
		return true
	default:
		return false
	}
}

// wrapTerminalResponseAlreadySent marks a turn that already persisted its own
// final message as ErrResponseAlreadySent.
//
// ci#1169: the terminal error must stay reachable for classification, because
// the queue task re-classifies it (failure text, and whether the turn settles as
// completed or failed). Re-wrapping with %v - the historic form - flattened the
// typed *toolCallCapError into a plain string, so a budget stop that the server
// performed by design reached the task as an opaque error and was classified
// (and settled) as an internal failure even though the turn is resumable.
//
// ci#1160: every classified terminal error is wrapped with %w for the same
// reason, not just the cap error. The historic %v form flattened the whole
// terminal error into a string, so the task re-classifying the turn saw an
// opaque error and rendered the generic "internal error" instead of the real
// reason (provider saturation, a provider fault, a cancelled context). With %w
// the user-visible text keeps the concrete reason *and* errors.Is/errors.As
// keep working downstream. The cap error behaves exactly as before.
func wrapTerminalResponseAlreadySent(err error) error {
	return fmt.Errorf("%w: %w", ErrResponseAlreadySent, err)
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
		// A failed-tool cap can be reached with successful calls in between;
		// say what already worked so the guidance stays actionable instead of
		// reading like a total failure.
		progress := ""
		if e.consumedTotal > e.consumedFailed {
			progress = fmt.Sprintf(
				" %d of the %d tool call(s) in this chat succeeded before that.",
				e.consumedTotal-e.consumedFailed, e.consumedTotal,
			)
		}
		return fmt.Sprintf(
			"It stopped after %d failed tool call(s), which is this chat's failed-tool budget (%d).%s Retry the failing step with different arguments (or a smaller scope), or send another message to continue from here - the work already done is kept.",
			e.consumedFailed, e.max, progress,
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
