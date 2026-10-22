package msgmate

import (
	"errors"
	"testing"
)

type fakeTurnContinuationScheduler struct {
	calls    int
	chatUUID string
	prompt   string
	delay    int
	corrID   string
	id       string
	err      error
}

func (f *fakeTurnContinuationScheduler) ScheduleCoderTurn(chatUUID, prompt string, delaySeconds int, correlationID string) (string, error) {
	f.calls++
	f.chatUUID, f.prompt, f.delay, f.corrID = chatUUID, prompt, delaySeconds, correlationID
	if f.err != nil {
		return "", f.err
	}
	return f.id, nil
}

func TestDispatchCapContinuationEnqueuesOnceForCapOutcome(t *testing.T) {
	scheduler := &fakeTurnContinuationScheduler{id: "sched-1"}
	outcome := &TurnOutcome{Reason: TurnEndedFailedToolCallCap}

	dispatch, err := DispatchCapContinuation(scheduler, "chat-1", outcome, false, 0, "ci#747", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dispatch.Scheduled || dispatch.ScheduleID != "sched-1" {
		t.Fatalf("expected one scheduled continuation, got %+v", dispatch)
	}
	if scheduler.calls != 1 {
		t.Fatalf("expected exactly one enqueue, got %d", scheduler.calls)
	}
	if scheduler.delay != TurnContinuationDelaySeconds {
		t.Fatalf("expected default delay %d, got %d", TurnContinuationDelaySeconds, scheduler.delay)
	}
	if scheduler.chatUUID != "chat-1" || scheduler.corrID != TurnContinuationCorrelationID("ci#747") {
		t.Fatalf("unexpected schedule target: %+v", scheduler)
	}
	if meta := CapContinuationVisibleMetadata(dispatch, "ci#747"); meta[TurnContinuationScheduledMetadataKey] != dispatch.Reason {
		t.Fatalf("continuation must be visible in the interaction, got %+v", meta)
	}
}

func TestDispatchCapContinuationIsBoundedByAttempts(t *testing.T) {
	scheduler := &fakeTurnContinuationScheduler{id: "sched-2"}
	outcome := &TurnOutcome{Reason: TurnEndedFailedToolCallCap}

	// A chain that already used its single continuation must not enqueue again.
	dispatch, err := DispatchCapContinuation(scheduler, "chat-1", outcome, false, 1, "ci#747", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dispatch.Scheduled || scheduler.calls != 0 {
		t.Fatalf("expected no second continuation, got %+v (%d calls)", dispatch, scheduler.calls)
	}
}

func TestDispatchCapContinuationRefusesUnidentifiableChain(t *testing.T) {
	scheduler := &fakeTurnContinuationScheduler{id: "sched-3"}
	outcome := &TurnOutcome{Reason: TurnEndedFailedToolCallCap}

	// Without a chain key the retry could not be de-duplicated (RC7), so it must
	// be refused rather than creating a duplicate interaction.
	dispatch, _ := DispatchCapContinuation(scheduler, "chat-1", outcome, false, 0, "", 0)
	if dispatch.Scheduled || scheduler.calls != 0 {
		t.Fatalf("expected refusal without a chain key, got %+v", dispatch)
	}
	// Same for a missing chat uuid: there is nowhere to enqueue it.
	dispatch, _ = DispatchCapContinuation(scheduler, "  ", outcome, false, 0, "ci#747", 0)
	if dispatch.Scheduled || scheduler.calls != 0 {
		t.Fatalf("expected refusal without a chat uuid, got %+v", dispatch)
	}
}

func TestDispatchCapContinuationSkipsCleanCompletion(t *testing.T) {
	scheduler := &fakeTurnContinuationScheduler{id: "sched-4"}
	outcome := &TurnOutcome{Reason: TurnEndedCompleted}

	dispatch, err := DispatchCapContinuation(scheduler, "chat-1", outcome, false, 0, "ci#747", 0)
	if err != nil || dispatch.Scheduled || scheduler.calls != 0 {
		t.Fatalf("a clean turn must not be continued: %+v (err %v)", dispatch, err)
	}
}

func TestDispatchCapContinuationReportsTransientInfraFailure(t *testing.T) {
	scheduler := &fakeTurnContinuationScheduler{id: "sched-5"}
	outcome := &TurnOutcome{Reason: TurnEndedToolCallCap}

	dispatch, err := DispatchCapContinuation(scheduler, "chat-1", outcome, true, 0, "ci#747", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dispatch.Scheduled || scheduler.delay != 5 {
		t.Fatalf("expected an explicit delay to be honoured, got %+v", dispatch)
	}
	if dispatch.Reason == "" {
		t.Fatal("a continuation must carry a machine-readable reason")
	}
}

func TestDispatchCapContinuationSurfacesEnqueueFailure(t *testing.T) {
	scheduler := &fakeTurnContinuationScheduler{err: errors.New("enqueue unavailable")}
	outcome := &TurnOutcome{Reason: TurnEndedFailedToolCallCap}

	dispatch, err := DispatchCapContinuation(scheduler, "chat-1", outcome, false, 0, "ci#747", 0)
	if err == nil {
		t.Fatal("a failed enqueue must not be swallowed (the interaction would stay waiting)")
	}
	if dispatch.Scheduled || dispatch.Reason == "" {
		t.Fatalf("the failed attempt must still be recorded explicitly, got %+v", dispatch)
	}
}
