package msgmate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// hugeOutputTool returns a fixed-size payload, standing in for a `coder_list`
// of the monorepo or a `coder_bash` dump (ci#680).
type hugeOutputTool struct {
	*BaseTool
	chars int
}

func (t *hugeOutputTool) RunTool(input interface{}) (string, error) {
	return strings.Repeat("x", t.chars), nil
}

// contextBudgetRequestServer answers the first toolRounds provider requests with
// one tool call for toolName and the final one with assistant text, recording
// the character size of every request it received.
func contextBudgetRequestServer(t *testing.T, toolName string, toolRounds int) (*httptest.Server, *[]int) {
	t.Helper()
	var (
		mu    sync.Mutex
		sizes []int
		round = 0
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]interface{}{}
		_ = json.Unmarshal(raw, &body)
		messages, _ := body["messages"].([]interface{})
		decoded := make([]map[string]interface{}, 0, len(messages))
		for _, entry := range messages {
			if message, ok := entry.(map[string]interface{}); ok {
				decoded = append(decoded, message)
			}
		}

		mu.Lock()
		round++
		current := round
		sizes = append(sizes, messagesCharCount(decoded))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if current <= toolRounds {
			_, _ = io.WriteString(w, sseFromToolCallChunks([]map[string]interface{}{
				toolCallChunk(fmt.Sprintf("call-%d", current), toolName, `{}`),
			}))
			return
		}
		_, _ = io.WriteString(w, sseFromToolCallChunks([]map[string]interface{}{
			{"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"content": "all done"}}}},
		}))
	}))
	t.Cleanup(server.Close)
	return server, &sizes
}

// TestTurnWithSeveralLargeToolOutputsStaysUnderTheBudgetAndCompletes is the
// ci#680 regression for the reported symptom: several tens-of-KB tool results in
// one turn used to overflow the provider window and kill the turn with
// "exceeded maximum context". With the context budget resolved for the turn,
// every request stays under the budget (older tool results are truncated and
// then evicted) and the turn finishes with its answer.
func TestTurnWithSeveralLargeToolOutputsStaysUnderTheBudgetAndCompletes(t *testing.T) {
	resetContextUsageRegistry()

	const toolChars = 40000
	tool := &hugeOutputTool{BaseTool: &BaseTool{ToolName: "coder_list"}, chars: toolChars}
	toolMap := map[string]Tool{"coder_list": tool}

	budget := ContextBudget{
		BudgetChars: 60000,
		Limits: ToolResultLimits{
			Default: 30000,
			PerTool: map[string]int{"coder_list": 30000},
			Source:  ToolResultLimitSourceConfig,
		},
	}
	outcome := &TurnOutcome{Reason: TurnEndedCompleted}
	server, sizes := contextBudgetRequestServer(t, "coder_list", 4)

	chunks, usage, toolCalls, errs := streamChatCompletion(
		context.Background(),
		server.URL, "some-model", "litellm",
		DefaultToolCallMaxTotal, DefaultToolCallMaxFailed,
		[]map[string]interface{}{
			{"role": "system", "content": "You are a coding agent."},
			{"role": "user", "content": "explore the repo"},
		},
		nil, toolMap, "test-key", nil, nil, nil, SamplingParams{}, ProviderRetryOptions{},
		providerRequestMeta{ChatUUID: "chat-680", Outcome: outcome, Context: budget},
	)

	var assistantText strings.Builder
	// The turn's error channel can deliver before the producer's deferred
	// channel closes run, so the reader goroutine is awaited explicitly:
	// reading the builder while the goroutine still appends both races on it
	// and can observe an empty answer (ci#680 flake).
	chunksDone := make(chan struct{})
	go func() {
		defer close(chunksDone)
		for chunk := range chunks {
			assistantText.WriteString(chunk)
		}
	}()
	go func() {
		for range usage {
		}
	}()
	callIDs := map[string]struct{}{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for frame := range toolCalls {
			if strings.TrimSpace(frame.Id) != "" {
				callIDs[frame.Id] = struct{}{}
			}
		}
	}()

	err := <-errs
	<-chunksDone
	<-done

	if err != nil {
		t.Fatalf("a turn with several large tool outputs must complete, got %v", err)
	}
	if !strings.Contains(assistantText.String(), "all done") {
		t.Fatalf("expected the turn's final answer, got %q", assistantText.String())
	}
	if len(callIDs) != 4 {
		t.Fatalf("expected the 4 tool calls the fixture issued, got %d", len(callIDs))
	}

	if len(*sizes) != 5 {
		t.Fatalf("expected 5 provider requests (4 tool rounds + the answer), got %d", len(*sizes))
	}
	for i, size := range *sizes {
		if size > budget.BudgetChars {
			t.Fatalf("request %d carried %d chars, over the %d-char context budget", i+1, size, budget.BudgetChars)
		}
	}

	// The per-tool budget must have cut the 40k results, and the turn's report
	// must say so (that is how the model learns to re-issue narrower calls).
	if outcome.Context.TruncatedToolResults == 0 {
		t.Fatal("expected the per-tool output budget to have truncated the large results")
	}
	if outcome.Context.BudgetChars != budget.BudgetChars {
		t.Fatalf("expected the report to carry the turn budget, got %d", outcome.Context.BudgetChars)
	}
	if outcome.Context.UsedChars > budget.BudgetChars {
		t.Fatalf("the reported usage (%d chars) exceeds the budget (%d)", outcome.Context.UsedChars, budget.BudgetChars)
	}
	if outcome.Context.EstimatedTokens <= 0 || outcome.Context.UsedPercent <= 0 {
		t.Fatalf("expected a token/usage estimate, got %#v", outcome.Context)
	}
	if outcome.Context.LargestToolResult != "coder_list" {
		t.Fatalf("expected the report to name the largest tool result, got %q", outcome.Context.LargestToolResult)
	}

	// The report is recorded per chat, so `coder_context` can answer for it.
	recorded, ok := ContextUsageFor("chat-680")
	if !ok {
		t.Fatal("expected the turn's context usage to be recorded for the chat")
	}
	if recorded.TruncatedToolResults == 0 {
		t.Fatalf("expected the recorded report to carry the truncation count, got %#v", recorded)
	}
}

// TestAppendExecutedToolCallsTruncatesLargeResultsExplicitly pins the per-tool
// output budget and its explicit marker: the model must see that the output was
// cut, by how much, and how to fetch the rest.
func TestAppendExecutedToolCallsTruncatesLargeResultsExplicitly(t *testing.T) {
	limits := ToolResultLimits{
		Default: 2000,
		PerTool: map[string]int{"coder_bash": 400, "coder_read": 2000},
		Source:  ToolResultLimitSourceConfig,
	}
	calls := []executedToolCall{
		{id: "call-big", name: "coder_bash", arguments: `{"command":"ls"}`, result: strings.Repeat("y", 5000), status: ToolCallStatusSucceeded},
		{id: "call-small", name: "coder_read", arguments: `{"path":"a.go"}`, result: "small", status: ToolCallStatusSucceeded},
	}

	appended, messages, truncated := appendExecutedToolCalls(nil, calls, map[string]bool{}, limits)
	if len(appended) != 2 || len(messages) != 4 {
		t.Fatalf("expected both calls appended with their result, got %d calls / %d messages", len(appended), len(messages))
	}
	if truncated != 1 {
		t.Fatalf("expected exactly one truncated result, got %d", truncated)
	}

	big, _ := messages[1]["content"].(string)
	if len(big) > 400 {
		t.Fatalf("the coder_bash result must be cut at its 400-char budget, got %d chars", len(big))
	}
	if !strings.Contains(big, "[tool output truncated:") || !strings.Contains(big, "coder_bash") {
		t.Fatalf("the truncation must be explicit and name the tool, got %q", big)
	}
	if !strings.Contains(big, "5000 chars") {
		t.Fatalf("the marker must report the original size, got %q", big)
	}
	for _, fragment := range []string{"offset/limit", "depth/limit"} {
		if !strings.Contains(big, fragment) {
			t.Fatalf("the marker must tell the model how to fetch the rest (%q), got %q", fragment, big)
		}
	}
	if small, _ := messages[3]["content"].(string); small != "small" {
		t.Fatalf("a result under its budget must be untouched, got %q", small)
	}
}

// TestTruncateToolResultKeepsTailAndCountsOmittedCommands pins the ci#731
// (T2/F2) fix: a `coder_bash` batch that exceeds its budget keeps a head AND a
// tail slice, and the marker names how many per-command results were dropped
// from the middle, so the conclusion of the batch survives the cut.
func TestTruncateToolResultKeepsTailAndCountsOmittedCommands(t *testing.T) {
	limits := ToolResultLimits{
		Default: 4000,
		PerTool: map[string]int{"coder_bash": 4000},
		Source:  ToolResultLimitSourceConfig,
	}
	batch := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"results":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(fmt.Sprintf(
				`{"index":%d,"exit_code":0,"stdout":"%s"}`, i, strings.Repeat(fmt.Sprintf("cmd-%d ", i), 200)))
		}
		b.WriteString(`],"tail_sentinel":"BATCH-DONE"}`)
		return b.String()
	}

	result := batch(20)
	truncated, ok := truncateToolResultForTool(result, "coder_bash", limits)
	if !ok {
		t.Fatal("expected a 20-command batch to be truncated")
	}
	if len(truncated) > 4000 {
		t.Fatalf("the truncated result must respect its budget, got %d chars", len(truncated))
	}
	if !strings.HasSuffix(strings.TrimRight(truncated, "\n"), `"tail_sentinel":"BATCH-DONE"}`) {
		t.Fatalf("the tail of the batch must survive the cut, got %q", truncated[len(truncated)-120:])
	}
	if !strings.Contains(truncated, "were omitted from the middle") ||
		!strings.Contains(truncated, "command result(s) dropped") {
		t.Fatalf("the marker must name the omitted middle and the dropped commands, got %q", truncated)
	}
	// A result that fits its budget is untouched.
	if plain, truncated := truncateToolResultForTool(`{"results":[]}`, "coder_bash", limits); truncated || plain != `{"results":[]}` {
		t.Fatalf("a small result must be untouched, got %q (truncated=%t)", plain, truncated)
	}
}

// TestResolveToolResultLimitsAppliesConfigAndEnvOverrides pins the
// configurability the ticket asks for: per-tool limits from the chat config, a
// deployment-wide env override that clamps them, and the compiled-in defaults
// otherwise.
func TestResolveToolResultLimitsAppliesConfigAndEnvOverrides(t *testing.T) {
	def := ResolveToolResultLimits(nil)
	if def.ForTool("coder_list") != 4000 || def.ForTool("coder_read") != DefaultToolResultMaxCharsForModel {
		t.Fatalf("unexpected compiled-in per-tool budgets: %#v", def.Metadata())
	}
	if def.Source != ToolResultLimitSourceDefault {
		t.Fatalf("expected the default source, got %q", def.Source)
	}
	// An unknown tool falls back to the default budget.
	if def.ForTool("coder_unknown") != DefaultToolResultMaxCharsForModel {
		t.Fatalf("expected the default budget for an unknown tool, got %d", def.ForTool("coder_unknown"))
	}

	configured := ResolveToolResultLimits(map[string]interface{}{
		ToolResultMaxCharsConfigKey: map[string]interface{}{
			"coder_list": 900,
			"default":    "1500",
		},
	})
	if configured.ForTool("coder_list") != 900 {
		t.Fatalf("expected the per-tool config entry to win, got %d", configured.ForTool("coder_list"))
	}
	if configured.ForTool("coder_bash") != 1500 {
		t.Fatalf("expected the string default to apply, got %d", configured.ForTool("coder_bash"))
	}
	if configured.Source != ToolResultLimitSourceConfig {
		t.Fatalf("expected the config source, got %q", configured.Source)
	}

	// An integer form sets the fallback for every tool.
	scalar := ResolveToolResultLimits(map[string]interface{}{ToolResultMaxCharsConfigKey: 2500})
	if scalar.ForTool("coder_grep") != 2500 || scalar.Default != 2500 {
		t.Fatalf("expected the scalar config to set the fallback, got %#v", scalar.Metadata())
	}

	// The env override is deployment-wide and clamps the per-tool limits.
	t.Setenv(ToolResultMaxCharsEnvKey, "800")
	env := ResolveToolResultLimits(map[string]interface{}{
		ToolResultMaxCharsConfigKey: map[string]interface{}{"coder_read": 100000},
	})
	if env.Source != ToolResultLimitSourceEnv {
		t.Fatalf("expected the env source, got %q", env.Source)
	}
	if env.ForTool("coder_read") != 800 {
		t.Fatalf("expected the env override to clamp the per-tool limit, got %d", env.ForTool("coder_read"))
	}
}

// TestResolveContextBudgetUsesConfigThenEnv covers the conversation budget
// resolution order.
func TestResolveContextBudgetUsesConfigThenEnv(t *testing.T) {
	if got := ResolveContextBudgetChars(nil); got != DefaultContextBudgetChars {
		t.Fatalf("expected the compiled-in budget, got %d", got)
	}
	if got := ResolveContextBudgetChars(map[string]interface{}{ContextBudgetCharsConfigKey: 50000}); got != 50000 {
		t.Fatalf("expected the config budget, got %d", got)
	}
	budget := ResolveContextBudget(map[string]interface{}{ContextBudgetCharsConfigKey: 1234}).WithDefaults()
	if budget.BudgetChars != 1234 || budget.Limits.ForTool("coder_list") != 4000 {
		t.Fatalf("expected both halves resolved, got %#v", budget.Metadata())
	}
	if zero := (ContextBudget{}).WithDefaults(); zero.BudgetChars != DefaultContextBudgetChars || zero.Limits.IsZero() {
		t.Fatalf("expected the zero budget to fall back to the defaults, got %#v", zero.Metadata())
	}
}

// TestContextBudgetEnvOverridesConfig pins that the deployment env wins over an
// explicitly configured chat budget (its own test, so t.Setenv cannot leak into
// the resolution-order assertions above).
func TestContextBudgetEnvOverridesConfig(t *testing.T) {
	t.Setenv(ContextBudgetCharsEnvKey, "70000")
	if got := ResolveContextBudgetChars(map[string]interface{}{ContextBudgetCharsConfigKey: 50000}); got != 70000 {
		t.Fatalf("expected the env budget to win, got %d", got)
	}
	t.Setenv(ContextBudgetCharsEnvKey, "not-a-number")
	if got := ResolveContextBudgetChars(map[string]interface{}{ContextBudgetCharsConfigKey: 50000}); got != 50000 {
		t.Fatalf("an unparsable env value must not override the config, got %d", got)
	}
}

// assertToolPairingValid checks the provider-facing invariant: every tool result
// answers a tool call declared before it, and every declared call has a result.
func assertToolPairingValid(t *testing.T, messages []map[string]interface{}) {
	t.Helper()
	declared := map[string]bool{}
	answered := map[string]bool{}
	for _, message := range messages {
		if id := toolCallIDOf(message); id != "" {
			answered[id] = true
		}
	}
	for _, message := range messages {
		switch roleOf(message) {
		case "assistant":
			for _, id := range toolCallIDsOf(message) {
				declared[id] = true
			}
		case "tool":
			id := toolCallIDOf(message)
			if id == "" || !declared[id] {
				t.Fatalf("orphaned tool result %q in %#v", id, messages)
			}
		}
	}
	for _, message := range messages {
		for _, id := range toolCallIDsOf(message) {
			if !answered[id] {
				t.Fatalf("declared tool call %q has no result in %#v", id, messages)
			}
		}
	}
}

// TestCompactMessagesToContextBudgetEvictsOldToolResultsAndKeepsThePairing is
// the guard's unit regression: a history of many large tool results is squeezed
// under the budget without touching the leading system message, the newest turn
// or a user message, and the result is still a request a provider accepts.
func TestCompactMessagesToContextBudgetEvictsOldToolResultsAndKeepsThePairing(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a coding agent."},
		{"role": "user", "content": "explore the repo"},
	}
	for i := 0; i < 12; i++ {
		callID := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			map[string]interface{}{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]interface{}{{
					"type": "function",
					"id":   callID,
					"function": map[string]interface{}{
						"name":      "coder_list",
						"arguments": `{"path":"."}`,
					},
				}},
			},
			map[string]interface{}{"role": "tool", "tool_call_id": callID, "content": strings.Repeat("z", 10000)},
			map[string]interface{}{"role": "assistant", "content": strings.Repeat("narration ", 200)},
		)
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": "now write the fix"})

	before := messagesCharCount(messages)
	compacted, stats, changed := compactMessagesToContextBudget(messages, 30000)
	if !changed {
		t.Fatalf("expected a %d-char history to be squeezed under a 30000-char budget", before)
	}
	after := messagesCharCount(compacted)
	if after > 30000 {
		t.Fatalf("expected the compacted history under the budget, got %d chars", after)
	}
	if stats.TotalCharsBefore != before || stats.TotalCharsAfter != after {
		t.Fatalf("expected the stats to describe the change, got %#v", stats)
	}
	if stats.EvictedToolResults == 0 || stats.EvictedNarration == 0 {
		t.Fatalf("expected older tool results and narration to be evicted, got %#v", stats)
	}

	if content, _ := compacted[0]["content"].(string); content != "You are a coding agent." {
		t.Fatalf("the leading system message must survive intact, got %q", content)
	}
	if content, _ := compacted[1]["content"].(string); content != "explore the repo" {
		t.Fatalf("a user message must never be evicted, got %q", content)
	}
	last := compacted[len(compacted)-1]
	if content, _ := last["content"].(string); content != "now write the fix" {
		t.Fatalf("the newest user turn must survive intact, got %q", content)
	}

	// The evicted tool results say so, and the request stays valid.
	evicted := 0
	for _, message := range compacted {
		content, _ := message["content"].(string)
		if strings.HasPrefix(content, "[tool output evicted from the context budget:") {
			evicted++
		}
	}
	if evicted == 0 {
		t.Fatal("expected at least one explicit eviction marker")
	}
	assertToolPairingValid(t, compacted)

	// An already-fitting history is returned untouched.
	unchanged, _, changed := compactMessagesToContextBudget([]map[string]interface{}{
		{"role": "user", "content": "hi"},
	}, 30000)
	if changed || len(unchanged) != 1 {
		t.Fatal("a history under the budget must not be touched")
	}
}

// TestCompactMessagesToContextBudgetIsIdempotent guards the loop's per-round
// call: compacting an already compacted history must be a no-op, so the guard
// cannot keep rewriting the same messages round after round.
func TestCompactMessagesToContextBudgetIsIdempotent(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "system"},
	}
	for i := 0; i < 20; i++ {
		callID := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			map[string]interface{}{
				"role": "assistant",
				"tool_calls": []map[string]interface{}{{
					"type": "function",
					"id":   callID,
					"function": map[string]interface{}{
						"name":      "coder_read",
						"arguments": `{}`,
					},
				}},
			},
			map[string]interface{}{"role": "tool", "tool_call_id": callID, "content": strings.Repeat("q", 5000)},
		)
	}
	compacted, _, changed := compactMessagesToContextBudget(messages, 20000)
	if !changed {
		t.Fatal("expected the history to be squeezed")
	}
	again, _, changedAgain := compactMessagesToContextBudget(compacted, 20000)
	if changedAgain {
		t.Fatalf("compacting an already compacted history must be a no-op, got %d -> %d chars", messagesCharCount(compacted), messagesCharCount(again))
	}
}

// TestCompactMessagesToContextBudgetNeverDropsTheNewestToolRound is the ci#728
// regression: a long turn whose recent tool call/result pair sits outside the
// protected tail used to have that result replaced by an eviction marker that
// told the model to re-issue the call - and the turn re-issued the very same
// call dozens of times (110 calls, 18 identical coder_read in chat
// ec46eb95-338d-4ff9-8781-2373eef2e974). The newest round must survive intact,
// and no eviction marker may ask for a re-issue.
func TestCompactMessagesToContextBudgetNeverDropsTheNewestToolRound(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a coding agent."},
		{"role": "user", "content": "read the file and fix it"},
	}
	// Plenty of older rounds that the guard is free to evict.
	for i := 0; i < 30; i++ {
		callID := fmt.Sprintf("old-call-%d", i)
		messages = append(messages,
			map[string]interface{}{
				"role": "assistant",
				"tool_calls": []map[string]interface{}{{
					"type":     "function",
					"id":       callID,
					"function": map[string]interface{}{"name": "coder_read", "arguments": `{"path":"old.go"}`},
				}},
			},
			map[string]interface{}{"role": "tool", "tool_call_id": callID, "content": strings.Repeat("o", 6000)},
		)
	}
	// The call the model just made, followed by an assistant narration and a
	// user message: more than contextBudgetProtectedTailMessages messages after
	// the call, so only the ci#728 rule can keep it.
	const recentCallID = "recent-call"
	const recentResult = "the recent result the model must still see"
	messages = append(messages,
		map[string]interface{}{
			"role": "assistant",
			"tool_calls": []map[string]interface{}{{
				"type":     "function",
				"id":       recentCallID,
				"function": map[string]interface{}{"name": "coder_read", "arguments": `{"path":"recent.go"}`},
			}},
		},
		map[string]interface{}{"role": "tool", "tool_call_id": recentCallID, "content": recentResult},
	)
	for i := 0; i < contextBudgetProtectedTailMessages+2; i++ {
		messages = append(messages, map[string]interface{}{"role": "assistant", "content": fmt.Sprintf("narration %d", i)})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": "continue"})

	before := messagesCharCount(messages)
	compacted, stats, changed := compactMessagesToContextBudget(messages, 30000)
	if !changed {
		t.Fatalf("expected a %d-char history to be squeezed under a 30000-char budget", before)
	}
	if after := messagesCharCount(compacted); after > 30000 {
		t.Fatalf("expected the compacted history under the budget, got %d chars", after)
	}

	// (a) The newest tool call and its result survive verbatim.
	callIndex, resultIndex := -1, -1
	for i, message := range compacted {
		if roleOf(message) == "tool" && toolCallIDOf(message) == recentCallID {
			resultIndex = i
		}
		for _, id := range toolCallIDsOf(message) {
			if id == recentCallID {
				callIndex = i
			}
		}
	}
	if callIndex < 0 {
		t.Fatalf("the most recent tool call %q must survive compaction, got %#v", recentCallID, compacted)
	}
	if resultIndex < 0 {
		t.Fatalf("the result of the most recent tool call %q must never be dropped, got %#v", recentCallID, compacted)
	}
	if content, _ := compacted[resultIndex]["content"].(string); content != recentResult {
		t.Fatalf("the most recent tool result must survive verbatim, got %q", content)
	}
	if stats.EvictedToolResults == 0 {
		t.Fatalf("expected the older tool results to be evicted, got %#v", stats)
	}
	assertToolPairingValid(t, compacted)

	// (b) No eviction marker tells the model to re-issue the call it just made.
	for _, message := range compacted {
		content, _ := message["content"].(string)
		if !strings.HasPrefix(content, "[tool output evicted from the context budget:") {
			continue
		}
		for _, fragment := range []string{"already ran", "Do NOT re-issue this call"} {
			if !strings.Contains(content, fragment) {
				t.Fatalf("the eviction marker must say the call already ran and must not be re-issued (%q missing), got %q", fragment, content)
			}
		}
		if strings.Contains(strings.ToLower(content), "re-issue the call with narrower arguments") {
			t.Fatalf("the eviction marker must not ask for a re-issue of the same call, got %q", content)
		}
	}
}

// TestCompactMessagesToContextBudgetReservesTheNotice covers the ci#822 r46
// root cause: the notice is merged into the leading system message *after* the
// guard ran, so the guard must reserve its length up front. A list that fits
// the raw budget but not the raw budget minus the reserve has to be compacted,
// and the compacted list plus the injected notice must still fit.
func TestCompactMessagesToContextBudgetReservesTheNotice(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a coding agent."},
	}
	for round := 0; round < 3; round++ {
		id := fmt.Sprintf("call-%d", round)
		messages = append(messages,
			map[string]interface{}{
				"role":    "assistant",
				"content": "reading",
				"tool_calls": []map[string]interface{}{{
					"type":     "function",
					"id":       id,
					"function": map[string]interface{}{"name": "coder_read", "arguments": `{"path":"a.go"}`},
				}},
			},
			map[string]interface{}{"role": "tool", "tool_call_id": id, "content": strings.Repeat("a", 20000)},
		)
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": "continue"})

	// A budget that the list fits exactly, but not once the notice is reserved:
	// this is the ci#822 r46 window (59889 of 60000 chars, sent at 60190).
	budget := messagesCharCount(messages) + 100
	if _, _, changed := compactMessagesToContextBudget(messages, budget); changed {
		t.Fatal("without a reserve the fitting list must not be rewritten")
	}

	compacted, stats, changed := compactMessagesToContextBudgetReserving(messages, budget, contextBudgetNoticeReserveChars)
	if !changed {
		t.Fatalf("the reserve must force a compaction, got %#v", stats)
	}
	if after := messagesCharCount(compacted); after > budget-contextBudgetNoticeReserveChars {
		t.Fatalf("the compacted list plus the notice reserve must fit the budget, got %d + %d > %d", after, contextBudgetNoticeReserveChars, budget)
	}

	// The outgoing request: compaction (with reserve) then notice injection must
	// stay within the budget the guard exists to enforce.
	final := injectContextBudgetNotice(compacted, ContextUsageReport{
		BudgetChars: budget,
		UsedChars:   messagesCharCount(compacted),
		UsedPercent: 90,
		Limits:      ResolveToolResultLimits(nil),
	})
	if got := messagesCharCount(final); got > budget {
		t.Fatalf("the request with the notice carried %d chars, over the %d-char budget", got, budget)
	}
}

// TestInjectContextBudgetNoticeIsThresholdedAndIdempotent covers the
// self-throttling notice: absent below the threshold, merged into the leading
// system message above it, and replaced (never stacked) on the next round.
func TestInjectContextBudgetNoticeIsThresholdedAndIdempotent(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a coding agent."},
		{"role": "user", "content": "go"},
	}
	low := ContextUsageReport{BudgetChars: 100000, UsedChars: 5000, UsedPercent: 5, Limits: ResolveToolResultLimits(nil)}
	if got := injectContextBudgetNotice(messages, low); len(got) != 2 {
		t.Fatal("below the threshold the notice must not be injected")
	}

	high := ContextUsageReport{BudgetChars: 100000, UsedChars: 90000, UsedPercent: 90, EstimatedTokens: 22500, TruncatedToolResults: 3, Limits: ResolveToolResultLimits(nil)}
	once := injectContextBudgetNotice(messages, high)
	if len(once) != 2 {
		t.Fatalf("expected a system + user message, got %d", len(once))
	}
	content, _ := once[0]["content"].(string)
	if !strings.Contains(content, "[context budget:") || !strings.Contains(content, "coder_context") {
		t.Fatalf("expected the self-throttling notice with a coder_context hint, got %q", content)
	}
	if !strings.Contains(content, "You are a coding agent.") {
		t.Fatalf("the system prompt must be preserved, got %q", content)
	}
	if !strings.Contains(content, "90%") {
		t.Fatalf("the notice must report the usage, got %q", content)
	}

	twice := injectContextBudgetNotice(once, high)
	contentAgain, _ := twice[0]["content"].(string)
	if strings.Count(contentAgain, "[context budget:") != 1 {
		t.Fatalf("the notice must be replaced, not stacked, got %q", contentAgain)
	}

	// A history without a leading system message gets one notice message, and
	// the model's own turns stay untouched.
	noSystem := injectContextBudgetNotice([]map[string]interface{}{{"role": "user", "content": "go"}}, high)
	if len(noSystem) != 2 || roleOf(noSystem[0]) != "system" || roleOf(noSystem[1]) != "user" {
		t.Fatalf("expected a leading system notice, got %#v", noSystem)
	}
}

// TestBuildContextUsageReportCountsToolResults covers the report the
// `coder_context` tool returns.
func TestBuildContextUsageReportCountsToolResults(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "system"},
		{
			"role":    "assistant",
			"content": "reading",
			"tool_calls": []map[string]interface{}{{
				"type":     "function",
				"id":       "call-1",
				"function": map[string]interface{}{"name": "coder_read", "arguments": `{"path":"a.go"}`},
			}},
		},
		{"role": "tool", "tool_call_id": "call-1", "content": strings.Repeat("a", 1000)},
	}
	report := BuildContextUsageReport(messages, 10000, ContextCompactionStats{MessagesBefore: 3, MessagesAfter: 3, EvictedToolResults: 1}, ResolveToolResultLimits(nil), 2)
	if report.ToolResultMessages != 1 || report.TotalToolResultChars != 1000 {
		t.Fatalf("expected one 1000-char tool result, got %#v", report)
	}
	if report.LargestToolResult != "coder_read" {
		t.Fatalf("expected the largest tool result to be named, got %q", report.LargestToolResult)
	}
	if report.UsedChars != messagesCharCount(messages) || report.UsedPercent != messagesCharCount(messages)*100/10000 {
		t.Fatalf("expected the usage to be measured, got %#v", report)
	}
	if report.TruncatedToolResults != 2 || report.EvictedToolResults != 1 {
		t.Fatalf("expected the guard counters in the report, got %#v", report)
	}
	meta := report.Metadata()
	if meta["budget_chars"] != 10000 || meta["largest_tool_result"] != "coder_read" {
		t.Fatalf("expected the metadata to carry the report, got %#v", meta)
	}
	limits, ok := meta["tool_result_max_chars"].(map[string]interface{})
	if !ok || limits["default"] != DefaultToolResultMaxCharsForModel {
		t.Fatalf("expected the effective tool-result limits in the report, got %#v", meta["tool_result_max_chars"])
	}
}

// TestContextUsageLookupFallsBackToBudgets covers the `coder_context` payload
// for a chat that has not produced a request yet: the effective budgets are
// still reported, and `measured` tells the model the numbers are not yet a
// measurement.
func TestContextUsageLookupFallsBackToBudgets(t *testing.T) {
	resetContextUsageRegistry()
	report, measured := contextUsageLookup("chat-unknown")
	if measured {
		t.Fatal("expected an unmeasured chat to report measured=false")
	}
	if report["budget_chars"] != DefaultContextBudgetChars {
		t.Fatalf("expected the effective budget, got %#v", report["budget_chars"])
	}

	RecordContextUsage("chat-known", ContextUsageReport{BudgetChars: 10, UsedChars: 5, UsedPercent: 50, Limits: ResolveToolResultLimits(nil)})
	report, measured = contextUsageLookup("chat-known")
	if !measured || report["used_chars"] != 5 {
		t.Fatalf("expected the recorded report, got %#v (measured=%t)", report, measured)
	}
	// An empty chat id must not panic or report a foreign chat's numbers.
	if _, ok := contextUsageLookup(""); ok {
		t.Fatal("an empty chat id must not report a measurement")
	}
}
