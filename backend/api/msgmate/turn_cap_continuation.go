package msgmate

import "fmt"

// ci#747 (RC2/RC3): a turn that exhausts its tool-call budget used to end as a
// hard failure. When the sandbox/gh/API is degraded an agent burns the whole
// failed-tool-call budget on retries and the interaction stops as `failed` with
// nothing re-firing it. The cap must instead degrade to a partial report plus
// *one* continuation, so the work resumes on its own and a human never has to
// re-dispatch it.
//
// This file owns the (pure) decision of that continuation: whether a terminal
// turn deserves one, which prompt it carries and under which stable correlation
// id it is enqueued. The id is stable for the same chain, so a repeated decision
// cancels the previous task instead of duplicating it (the dedupe rule of RC7).
const (
	// TurnContinuationMaxAttempts is how many automatic continuations a single
	// chain may enqueue. Exactly one: a cap exhaustion is followed by one retry.
	// If that retry exhausts the cap again the chain stops and the failure stays
	// explicit in the interaction and in the tracker - which is the acceptance
	// of ci#747 ("recover without a human re-dispatch, or the failure is
	// explicit").
	TurnContinuationMaxAttempts = 1

	// TurnContinuationPrompt is the prompt of a continuation after a tool-call
	// cap. It forbids repeating the failing calls, which is what burned the
	// budget in the first place (RC2).
	TurnContinuationPrompt = "Your previous turn stopped because it exhausted its tool-call budget while calls kept failing. " +
		"Do not repeat the same failing calls: name the blocker (sandbox, gh, provider), record it in the tracker and continue with the remaining work. " +
		"If the blocker is environmental, report it explicitly instead of retrying."

	// TurnContinuationTransientPrompt is the prompt of a continuation after a
	// transient infrastructure failure (ci#688 transient tool failure, ci#691
	// sandbox capacity, ci#747 RC3 provider 402/429/5xx).
	TurnContinuationTransientPrompt = "Your previous turn stopped on a transient infrastructure failure (provider or workspace saturation). " +
		"It is expected to have recovered: resume the work where it stopped and report status."

	// ContinuationCorrelationPrefix namespaces the correlation id so it can
	// never collide with a user-supplied coder_schedule correlation id.
	ContinuationCorrelationPrefix = "ci747-cont"
)

// Reasons of an automatic continuation. They travel as metadata so the retry is
// visible in the interaction (RC3: never silently stay waiting).
const (
	TurnContinuationFailedToolCap = "failed_tool_call_cap_reached"
	TurnContinuationToolCap       = "tool_call_cap_reached"
	TurnContinuationTransient     = "provider_or_workspace_transient"
)

// TurnContinuation is the decision to re-fire a turn that ended on a cap or on a
// transient infrastructure failure.
//
// The zero value means "no continuation": the turn ended for a reason that a
// retry cannot fix, so the failure has to be explicit instead of being masked by
// an automatic retry.
//
// It is deliberately a pure value: the caller (the tool loop / the interaction
// handler) owns the enqueue, so this decision stays unit-testable without a
// provider, a database or a queue.
type TurnContinuation struct {
	// Required is true when exactly one continuation must be enqueued.
	Required bool
	// Reason is the machine-readable marker of the continuation (see the
	// TurnContinuation* reasons), recorded as metadata of the continuation.
	Reason string
	// Prompt is the text the continuation carries.
	Prompt string
	// CorrelationID is stable for one chain, so a repeated decision cancels the
	// previous task (same task id) instead of duplicating it (RC7/QW4).
	CorrelationID string
}

// turnContinuationNone is the explicit "do not continue" decision. Kept as a
// function so the intent is readable at the call sites and testable as the zero
// value.
func turnContinuationNone() TurnContinuation {
	return TurnContinuation{}
}

// CapContinuationForOutcome decides whether a finished turn is continued
// automatically.
//
// transientInfra is true when the turn hit a failure a retry can plausibly fix:
// a budget-exempt transient tool failure (ci#688) or a workspace that could not
// be scheduled (ci#691). It is passed in instead of being read off the outcome so
// the decision stays a pure function of its inputs (and so the classification
// itself stays owned by the turn loop that observed it).
//
// attempts is how many continuations this chain already enqueued, so the chain is
// bounded by TurnContinuationMaxAttempts. chainKey identifies the chain (the
// interaction or the ticket+lane owner key); an empty chainKey yields no
// correlation id and therefore no continuation, because an unidentified retry
// could not be de-duplicated.
func CapContinuationForOutcome(outcome *TurnOutcome, transientInfra bool, attempts int, chainKey string) TurnContinuation {
	if outcome == nil || attempts >= TurnContinuationMaxAttempts || chainKey == "" {
		return turnContinuationNone()
	}
	switch outcome.reason() {
	case TurnEndedFailedToolCallCap:
		return newTurnContinuation(TurnContinuationFailedToolCap, TurnContinuationPrompt, chainKey)
	case TurnEndedToolCallCap:
		// A total-cap exhaustion with no failure is a turn that simply ran out
		// of budget while doing work: continue it once.
		return newTurnContinuation(TurnContinuationToolCap, TurnContinuationPrompt, chainKey)
	case TurnEndedProviderError:
		if !transientInfra {
			// A provider error without a single transient failure is not
			// recoverable by retrying the same turn.
			return turnContinuationNone()
		}
		return newTurnContinuation(TurnContinuationTransient, TurnContinuationTransientPrompt, chainKey)
	default:
		return turnContinuationNone()
	}
}

// newTurnContinuation builds a required continuation for one chain.
func newTurnContinuation(reason, prompt, chainKey string) TurnContinuation {
	return TurnContinuation{
		Required:      true,
		Reason:        reason,
		Prompt:        prompt,
		CorrelationID: TurnContinuationCorrelationID(chainKey),
	}
}

// TurnContinuationCorrelationID derives the stable correlation id of one
// continuation chain. Stability is what makes the retry idempotent: a newer
// decision for the same chain cancels the previous task instead of adding a
// second one.
func TurnContinuationCorrelationID(chainKey string) string {
	return fmt.Sprintf("%s-%s", ContinuationCorrelationPrefix, chainKey)
}
