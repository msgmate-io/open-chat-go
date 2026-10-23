package msgmate

import (
	"testing"

	"backend/nativecoder"
)

// TestToolCallBudgetExemptionIsBookkeepingOnly is the ci#724 rule: scheduling the
// next pulse is bookkeeping, so `coder_schedule` does not consume the turn's
// total tool-call budget, while ordinary work does.
func TestToolCallBudgetExemptionIsBookkeepingOnly(t *testing.T) {
	if !toolCallBudgetExempt("coder_schedule") {
		t.Fatal("coder_schedule must be exempt from the turn's total tool-call budget")
	}
	for _, name := range []string{"coder_bash", "coder_edit", "coder_read", "coder_webfetch", ""} {
		if toolCallBudgetExempt(name) {
			t.Fatalf("%q does work and must consume the total tool-call budget", name)
		}
	}
}

// TestToolCallBudgetAccountingSeparatesExemptCalls proves the counter rule: the
// exempt bookkeeping call is reported separately and is never charged to the
// cap, so a turn that re-arms itself cannot shorten the turn that scheduled it.
// The failed-tool budget is untouched — a schedule that cannot be enqueued is a
// real failure and still counts as one.
func TestToolCallBudgetAccountingSeparatesExemptCalls(t *testing.T) {
	calls := []executedToolCall{
		{name: "coder_read", status: ToolCallStatusSucceeded},
		{name: "coder_schedule", status: ToolCallStatusSucceeded, turnBudgetExempt: true},
		{name: "coder_schedule", status: ToolCallStatusSucceeded, turnBudgetExempt: true},
		{name: "coder_bash", status: ToolCallStatusFailed},
		{name: "coder_schedule", status: ToolCallStatusFailed},
	}

	counted, exempt := toolCallBudgetAccounting(calls, 0)
	if counted != 3 {
		t.Fatalf("expected 3 charged calls, got %d", counted)
	}
	if exempt != 2 {
		t.Fatalf("expected 2 budget-exempt calls, got %d", exempt)
	}

	failed, transient := toolCallFailureAccounting(calls)
	if failed != 2 {
		t.Fatalf("expected the failed-tool budget to stay untouched by the exemption, got %d", failed)
	}
	if transient != 0 {
		t.Fatalf("expected no transient failure to be claimed, got %d", transient)
	}
}

// TestToolCallBudgetAccountingWithoutExemptCalls guards the default: a turn that
// schedules nothing is charged exactly as before ci#724.
func TestToolCallBudgetAccountingWithoutExemptCalls(t *testing.T) {
	calls := []executedToolCall{
		{name: "coder_read", status: ToolCallStatusSucceeded},
		{name: "coder_bash", status: ToolCallStatusSucceeded},
	}

	counted, exempt := toolCallBudgetAccounting(calls, 0)
	if counted != 2 || exempt != 0 {
		t.Fatalf("expected every call to be charged, got counted=%d exempt=%d", counted, exempt)
	}

	if counted, exempt := toolCallBudgetAccounting(nil, 0); counted != 0 || exempt != 0 {
		t.Fatalf("expected an empty round to charge nothing, got counted=%d exempt=%d", counted, exempt)
	}
}

// TestToolCallBudgetExemptionIsCappedPerTurn is the safety net of the ci#724
// exemption: exempting a tool must never mean "unbounded". A turn that keeps
// scheduling once it has spent its allowance is charged like ordinary work, so
// the tool-call cap still terminates it.
func TestToolCallBudgetExemptionIsCappedPerTurn(t *testing.T) {
	calls := []executedToolCall{
		{name: "coder_schedule", status: ToolCallStatusSucceeded, turnBudgetExempt: true},
		{name: "coder_schedule", status: ToolCallStatusSucceeded, turnBudgetExempt: true},
	}

	// At the allowance the calls are charged instead of escaping the cap.
	counted, exempt := toolCallBudgetAccounting(calls, toolCallBudgetExemptLimit)
	if counted != 2 || exempt != 0 {
		t.Fatalf("expected the turn to be charged once the allowance is spent, got counted=%d exempt=%d", counted, exempt)
	}

	// One slot left: only that one call is exempt, the next is charged.
	counted, exempt = toolCallBudgetAccounting(calls, toolCallBudgetExemptLimit-1)
	if counted != 1 || exempt != 1 {
		t.Fatalf("expected the allowance to be spent exactly once, got counted=%d exempt=%d", counted, exempt)
	}
}

// TestTurnOutcomeReportsBudgetExemptCalls checks that the exemption reaches the
// turn outcome and its usage metadata, so a self-re-arming turn is readable from
// the chat instead of only from the backend log.
func TestTurnOutcomeReportsBudgetExemptCalls(t *testing.T) {
	outcome := &TurnOutcome{
		Reason:            TurnEndedToolCallCap,
		ToolCalls:         12,
		BudgetExemptCalls: 3,
		Budget:            nativecoder.ToolCallBudget{MaxTotal: 12, MaxFailed: 3, Source: nativecoder.ToolCallBudgetSourceChatSnapshot},
	}

	usage := outcome.usage()
	if usage.ConsumedTotal != 12 {
		t.Fatalf("expected the charged calls only, got %+v", usage)
	}
	if usage.BudgetExemptTotal != 3 {
		t.Fatalf("expected the budget-exempt call count on the usage, got %+v", usage)
	}

	metadata := usage.Metadata()
	if metadata["consumed_total"] != 12 || metadata["budget_exempt"] != 3 {
		t.Fatalf("expected the budget-exempt count in the usage metadata, got %#v", metadata)
	}

	// A nil outcome keeps reporting zeros rather than panicking.
	if nilUsage := (*TurnOutcome)(nil).usage(); nilUsage.BudgetExemptTotal != 0 {
		t.Fatalf("expected a nil outcome to report no exempt calls, got %+v", nilUsage)
	}
}
