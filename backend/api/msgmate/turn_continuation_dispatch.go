package msgmate

import (
	"fmt"
	"strings"
)

// ci#747 (RC2/RC3): the continuation *decision* (turn_cap_continuation.go) is
// pure and therefore was never dispatched: nothing turned "this turn ended on a
// cap or on a transient infra failure" into an actual re-fire, so the
// interaction stayed `waiting` until a human re-dispatched it.
//
// This file is the dispatch seam. It reuses the existing in-product scheduler
// (`coder_schedule`, ci#724) instead of an external CronJob, so the retry is an
// ordinary scheduled turn of the same chat: it is visible in the interaction,
// it carries the stable correlation id of the chain (a newer decision cancels
// the previous task instead of adding a second one, RC7/QW4) and it needs no
// provider, database or queue of its own.
const (
	// TurnContinuationDelaySeconds is how long a continuation waits before it
	// fires. It is short enough that a recovered provider or workspace is picked
	// up promptly and long enough to let a saturation clear (RC3).
	TurnContinuationDelaySeconds = 45

	// TurnContinuationScheduledMetadataKey marks the scheduled turn as an
	// automatic continuation so the transcript can tell it apart from a
	// user-scheduled turn and a human can see *why* the agent resumed.
	TurnContinuationScheduledMetadataKey = "ci747_continuation_reason"
)

// turnContinuationScheduler is the narrow slice of the coder_schedule runner the
// dispatch needs. Declaring it here keeps the dispatch testable with a fake and
// proves at compile time (see the assertion below) that the real scheduler
// satisfies it.
type turnContinuationScheduler interface {
	ScheduleCoderTurn(chatUUID string, prompt string, delaySeconds int, correlationID string) (string, error)
}

// The real in-product scheduler must satisfy the seam: if its signature drifts,
// the automatic continuation silently stops being dispatchable, which is the
// exact class of failure this ticket tracks.
var _ turnContinuationScheduler = coderScheduleRunner{}

// TurnContinuationDispatch is the outcome of dispatching a continuation.
type TurnContinuationDispatch struct {
	// Scheduled is true when a continuation task was enqueued.
	Scheduled bool
	// ScheduleID is the task id of the enqueued continuation (empty otherwise).
	ScheduleID string
	// Reason is the machine-readable continuation reason, so the caller can
	// record it as metadata and the retry stays visible (RC3).
	Reason string
}

// DispatchCapContinuation decides and enqueues at most one automatic
// continuation for a finished turn.
//
// It returns Scheduled=false (and never calls the scheduler) when the outcome
// does not deserve a retry, when the chain already used its single continuation,
// or when the chain is unidentified - an unidentifiable retry could not be
// de-duplicated, so refusing it is what keeps "one owner per ticket+lane" true
// (RC7).
//
// delaySeconds <= 0 falls back to TurnContinuationDelaySeconds.
func DispatchCapContinuation(scheduler turnContinuationScheduler, chatUUID string, outcome *TurnOutcome, transientInfra bool, attempts int, chainKey string, delaySeconds int) (TurnContinuationDispatch, error) {
	chatUUID = strings.TrimSpace(chatUUID)
	continuation := CapContinuationForOutcome(outcome, transientInfra, attempts, chainKey)
	if !continuation.Required || chatUUID == "" || scheduler == nil {
		return TurnContinuationDispatch{}, nil
	}
	if delaySeconds <= 0 {
		delaySeconds = TurnContinuationDelaySeconds
	}
	scheduleID, err := scheduler.ScheduleCoderTurn(chatUUID, continuation.Prompt, delaySeconds, continuation.CorrelationID)
	if err != nil {
		// A failed enqueue must not be swallowed: the caller records it so the
		// interaction shows an explicit blocker instead of staying waiting.
		return TurnContinuationDispatch{Reason: continuation.Reason}, err
	}
	return TurnContinuationDispatch{
		Scheduled:  true,
		ScheduleID: scheduleID,
		Reason:     continuation.Reason,
	}, nil
}

// CapContinuationVisibleMetadata renders the metadata of a dispatched
// continuation. It is what makes the retry visible in the interaction instead of
// the agent silently staying `waiting` (RC3), and it carries the correlation id
// so a repeated decision is recognisable as the same chain.
func CapContinuationVisibleMetadata(dispatch TurnContinuationDispatch, chainKey string) map[string]interface{} {
	if !dispatch.Scheduled {
		return nil
	}
	return map[string]interface{}{
		TurnContinuationScheduledMetadataKey: dispatch.Reason,
		"continuation_correlation_id":        TurnContinuationCorrelationID(chainKey),
		"continuation_schedule_id":           dispatch.ScheduleID,
	}
}

// ContinuationAttemptsFromMetadata reports how many automatic continuations the
// chain that produced this turn already used. The scheduled continuation carries
// its stable correlation id as coder_schedule metadata; the first continuation
// therefore sees attempts=0 and its scheduled turn sees attempts=1, which is what
// bounds a chain to TurnContinuationMaxAttempts (ci#747 RC2/RC3).
func ContinuationAttemptsFromMetadata(metadata map[string]interface{}) int {
	if metadata == nil {
		return 0
	}
	if raw, ok := metadata["ci747_continuation_attempts"]; ok {
		if n, ok := metadataInt(raw); ok && n > 0 {
			return n
		}
	}
	if correlation, _ := metadata["coder_schedule_scheduled_by_correlation"].(string); strings.HasPrefix(correlation, ContinuationCorrelationPrefix+"-") {
		return 1
	}
	return 0
}

func metadataInt(raw interface{}) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		var parsed int
		if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}
