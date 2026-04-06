package msgmate

// ci#724: some tools are bookkeeping rather than work, so they must not consume
// the turn's total tool-call budget. `coder_schedule` is the first one: a lead
// agent that re-arms its own next pulse must not shorten the turn it is
// currently running (ci#724 requires the tool to be budget-exempt). The
// failed-tool budget is deliberately *not* affected — a schedule that cannot be
// enqueued is a real failure the agent should see.
var budgetExemptToolNames = map[string]bool{
	"coder_schedule": true,
}

// toolCallBudgetExempt reports whether a call of that tool name is exempt from
// the turn's total tool-call budget.
func toolCallBudgetExempt(name string) bool {
	return budgetExemptToolNames[name]
}

// toolCallBudgetAccounting splits one round's executed calls into the calls that
// consume the turn's total tool-call budget and the budget-exempt ones. Keeping
// it a pure function makes the exemption rule unit-testable without a provider
// (the toolCallFailureAccounting pattern of ci#688).
func toolCallBudgetAccounting(calls []executedToolCall) (counted, exempt int) {
	for _, call := range calls {
		if call.turnBudgetExempt {
			exempt++
			continue
		}
		counted++
	}
	return counted, exempt
}
