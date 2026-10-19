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

// toolCallBudgetExemptLimit caps how many calls of a single turn may escape the
// total tool-call budget as bookkeeping. Exempting a tool must never mean
// "unbounded": a turn that loops on scheduling eventually pays for it like any
// other work, so the turn still terminates on the same cap as before ci#724.
const toolCallBudgetExemptLimit = 24

// toolCallBudgetExempt reports whether a call of that tool name is eligible for
// the turn's total tool-call budget exemption.
func toolCallBudgetExempt(name string) bool {
	return budgetExemptToolNames[name]
}

// toolCallBudgetAccounting splits one round's executed calls into the calls that
// consume the turn's total tool-call budget and the budget-exempt ones.
// alreadyExempt is how many calls of this turn were exempted before this round;
// once the turn reaches toolCallBudgetExemptLimit the remaining bookkeeping calls
// are charged like ordinary work, which keeps the turn bounded. Keeping it a
// pure function makes the rule unit-testable without a provider (the
// toolCallFailureAccounting pattern of ci#688).
func toolCallBudgetAccounting(calls []executedToolCall, alreadyExempt int) (counted, exempt int) {
	for _, call := range calls {
		if call.turnBudgetExempt && alreadyExempt+exempt < toolCallBudgetExemptLimit {
			exempt++
			continue
		}
		counted++
	}
	return counted, exempt
}
