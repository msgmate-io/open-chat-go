package msgmate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"backend/logging"
)

// Context budgeting (ci#680).
//
// Tool results used to be appended to the conversation verbatim. A single
// `coder_list` of the monorepo returns ~380 entries, a full-file `coder_read`
// or a `coder_bash` dump tens of KB, so a few exploratory calls pushed the
// request past the provider window and the turn died with "exceeded maximum
// context" - the agent's work was lost and the interaction had to be restarted.
//
// This file owns the three guards that stop that:
//
//  1. a *per-tool output budget*: every tool result appended to the transcript
//     is hard-truncated at its own limit, with an explicit marker that names the
//     tool, the original size and how to fetch the missing part;
//  2. a *conversation budget guard*: before a request is sent, the history is
//     squeezed under the character budget by evicting older tool results and
//     intermediate narration - and, as a last resort, dropping whole old
//     messages - repairing the tool-call/tool-result pairing afterwards so the
//     retried request stays valid;
//  3. a *usage report* (`ContextUsageReport`) recorded per chat, surfaced as
//     message metadata (`context_budget`) and returned by the `coder_context`
//     tool, so an agent (and the loop) can self-throttle before the window is
//     exhausted.

const (
	// DefaultContextBudgetChars is the compiled-in character budget of one
	// outgoing conversation. ~120k chars is a conservative ~30k tokens, which
	// leaves room for the system prompt, the tool definitions and the answer in
	// a 64k+ token window.
	DefaultContextBudgetChars = 120000

	// ToolResultMaxCharsEnvKey and ContextBudgetCharsEnvKey are the
	// deployment-level overrides, using the same OCI_MSGMATE_CODER_ prefix as
	// the other native-coder settings.
	ToolResultMaxCharsEnvKey = "OCI_MSGMATE_CODER_TOOL_RESULT_MAX_CHARS"
	ContextBudgetCharsEnvKey = "OCI_MSGMATE_CODER_CONTEXT_BUDGET_CHARS"

	// ToolResultMaxCharsConfigKey and ContextBudgetCharsConfigKey are the
	// per-chat / per-bot shared-config keys. The first accepts either an integer
	// (the fallback limit for every tool) or an object mapping a tool name to
	// its limit plus an optional `default` entry.
	ToolResultMaxCharsConfigKey = "tool_result_max_chars"
	ContextBudgetCharsConfigKey = "context_budget_chars"

	// Derived from the assumed provider window: the report prints an estimated
	// token count so an agent can compare it against its model.
	DefaultModelContextWindowTokens = 128000
	// CharsPerTokenEstimate is the deliberately conservative chars-per-token
	// factor used for the estimate (code and JSON are token-dense).
	CharsPerTokenEstimate = 4

	// contextBudgetTargetRatioPercent is the share of the budget the guard aims
	// for once it has to evict, leaving headroom for the next round's tool
	// output.
	contextBudgetTargetRatioPercent = 90

	// contextBudgetProtectedTailMessages is how many of the newest messages are
	// never evicted or dropped on the first pass: the model needs its most
	// recent turns to continue the work.
	contextBudgetProtectedTailMessages = 8

	// contextBudgetHugeMessageChars is the size above which a non-newest user
	// message is truncated rather than left intact (a pasted log, not a task).
	contextBudgetHugeMessageChars = 8000
	// contextBudgetUserMessageKeepChars is the head of such a user message that
	// survives the truncation.
	contextBudgetUserMessageKeepChars = 2000

	// contextBudgetNoticeThresholdPercent is the used-percent above which a
	// one-line budget notice is merged into the system prompt, so the model
	// self-throttles (targeted reads, shallow listings) before the window is
	// exhausted.
	contextBudgetNoticeThresholdPercent = 60
)

// Tool-result budget origins, reported as structured state.
const (
	ToolResultLimitSourceDefault = "default"
	ToolResultLimitSourceConfig  = "config"
	ToolResultLimitSourceEnv     = "env"
)

// toolResultMaxCharsDefaults are the per-tool output budgets (ci#680). The
// values mirror how the tool is used: a listing or a grep is cheap to re-issue
// and very expensive in context, so it gets the tightest cap; a read is the
// tool an agent uses to inspect code, so it keeps the most room.
var toolResultMaxCharsDefaults = map[string]int{
	"coder_list": 4000,
	"coder_glob": 4000,
	"coder_grep": 6000,
	"coder_bash": 8000,
	"coder_git":  8000,
	"coder_read": DefaultToolResultMaxCharsForModel,
}

// ToolResultLimits is the effective per-tool output budget of a chat: the
// fallback limit every tool without its own entry gets, the per-tool overrides
// and where the limits came from.
type ToolResultLimits struct {
	Default int            `json:"default"`
	PerTool map[string]int `json:"per_tool,omitempty"`
	Source  string         `json:"source"`
}

// Metadata renders the limits as JSON-friendly state, so a consumer sees the
// effective budgets instead of only the truncation marker.
func (l ToolResultLimits) Metadata() map[string]interface{} {
	perTool := map[string]interface{}{}
	for name, limit := range l.PerTool {
		perTool[name] = limit
	}
	return map[string]interface{}{
		"default":  l.ForTool(""),
		"per_tool": perTool,
		"source":   l.Source,
	}
}

// IsZero reports whether the limits were never resolved, so a caller can fall
// back to the compiled-in defaults.
func (l ToolResultLimits) IsZero() bool {
	return l.Default <= 0 && len(l.PerTool) == 0
}

// ForTool returns the output budget of one tool: its own limit when the config
// declares one, the fallback limit otherwise.
func (l ToolResultLimits) ForTool(toolName string) int {
	if limit, ok := l.PerTool[strings.TrimSpace(toolName)]; ok && limit > 0 {
		return limit
	}
	if l.Default > 0 {
		return l.Default
	}
	return DefaultToolResultMaxCharsForModel
}

// ResolveToolResultLimits resolves the effective per-tool output budgets from
// the per-chat shared config and the deployment env, over the compiled-in
// defaults. A per-tool entry always wins; the fallback limit clamps every
// per-tool limit, so a deployment-wide reduction applies everywhere.
func ResolveToolResultLimits(config map[string]interface{}) ToolResultLimits {
	limits := ToolResultLimits{
		Default: DefaultToolResultMaxCharsForModel,
		PerTool: map[string]int{},
		Source:  ToolResultLimitSourceDefault,
	}
	for name, limit := range toolResultMaxCharsDefaults {
		limits.PerTool[name] = limit
	}

	if config != nil {
		if raw, exists := config[ToolResultMaxCharsConfigKey]; exists && raw != nil {
			switch typed := raw.(type) {
			case map[string]interface{}:
				for name, value := range typed {
					limit, ok := positiveIntFromValue(value)
					if !ok {
						continue
					}
					key := strings.TrimSpace(name)
					if key == "" {
						continue
					}
					if key == "default" {
						limits.Default = limit
						limits.Source = ToolResultLimitSourceConfig
						continue
					}
					limits.PerTool[key] = limit
					limits.Source = ToolResultLimitSourceConfig
				}
			default:
				if limit, ok := positiveIntFromValue(typed); ok {
					limits.Default = limit
					limits.Source = ToolResultLimitSourceConfig
				}
			}
		}
	}

	if raw := strings.TrimSpace(os.Getenv(ToolResultMaxCharsEnvKey)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limits.Default = parsed
			limits.Source = ToolResultLimitSourceEnv
		}
	}

	for name, limit := range limits.PerTool {
		if limits.Default > 0 && limit > limits.Default {
			limits.PerTool[name] = limits.Default
		}
	}
	return limits
}

// ResolveContextBudgetChars resolves the character budget of one outgoing
// conversation (per-chat shared config, then the deployment env, then the
// compiled-in default).
func ResolveContextBudgetChars(config map[string]interface{}) int {
	budget := DefaultContextBudgetChars
	if config != nil {
		if limit, ok := positiveIntFromValue(config[ContextBudgetCharsConfigKey]); ok {
			budget = limit
		}
	}
	if raw := strings.TrimSpace(os.Getenv(ContextBudgetCharsEnvKey)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			budget = parsed
		}
	}
	return budget
}

// EstimateTokensForChars converts a character count into the conservative token
// estimate the report prints.
func EstimateTokensForChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	estimate := chars / CharsPerTokenEstimate
	if estimate < 1 {
		return 1
	}
	return estimate
}

// truncateToolResultForTool hard-truncates one tool result at the tool's output
// budget (ci#680). The marker is explicit: it names the tool, the original size
// and the kept size, and it tells the model how to fetch the missing part
// instead of leaving it guessing why the output ends mid-stream.
func truncateToolResultForTool(result, toolName string, limits ToolResultLimits) (string, bool) {
	limit := limits.ForTool(toolName)
	if limit <= 0 || len(result) <= limit {
		return result, false
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	// ci#731 (T2/F2): a head-only cut ends the output mid-stream and drops the
	// tail of a `coder_bash` batch - exactly where the trailing commands of a
	// `commands: [...]` batch live (2 of 5 reference batches lost trailing
	// commands entirely). Keep a head slice AND a tail slice, and name the
	// omitted middle (chars plus the coder_bash command results that fell into
	// it) so the conclusion of the batch survives the cut.
	const markerReserve = 380
	contentBudget := limit - markerReserve
	if contentBudget < 0 {
		contentBudget = 0
	}
	headChars := contentBudget * 3 / 5
	tailChars := contentBudget - headChars
	head := headOfString(result, headChars)
	tail := ""
	if tailChars > 0 && len(result) > len(head) {
		available := len(result) - len(head)
		if tailChars > available {
			tailChars = available
		}
		tail = result[len(result)-tailChars:]
	}
	middle := result[len(head) : len(result)-len(tail)]
	notice := fmt.Sprintf(
		"\n\n[tool output truncated: %s returned %d chars; %d were omitted from the middle (per-tool budget "+
			"%d)%s. Re-issue with narrower arguments - coder_read offset/limit, coder_list depth/limit, "+
			"coder_grep include/paths - or redirect the output to a file and read the ranges you need.]",
		name, len(result), len(middle), limit, omittedCommandNote(middle),
	)
	out := head + notice + tail
	if excess := len(out) - limit; excess > 0 {
		if excess >= len(tail) {
			excess -= len(tail)
			tail = ""
		} else {
			tail = tail[excess:]
			excess = 0
		}
		if excess > 0 {
			if excess >= len(head) {
				head = ""
			} else {
				head = head[:len(head)-excess]
			}
		}
		out = head + notice + tail
		// A budget too small to hold the marker itself: keep the leading part of
		// the marker (it still names the tool and the original size) rather than
		// blowing the budget the guard exists to enforce.
		if excess = len(out) - limit; excess > 0 {
			out = headOfString(notice, len(notice)-excess)
		}
	}
	return out, true
}

// omittedCommandNote names the coder_bash command results that fell into the
// middle slice of a truncated batch result (ci#731). It is a cheap,
// envelope-agnostic count: every per-command result of a `commands: [...]`
// batch carries an `exit_code` key, so counting that key in the dropped middle
// is enough to tell the agent how many commands vanished instead of leaving it
// to guess why the batch ends early. It returns "" when nothing was dropped.
func omittedCommandNote(middle string) string {
	dropped := 0
	for rest := middle; ; {
		idx := strings.Index(rest, `"exit_code"`)
		if idx < 0 {
			break
		}
		dropped++
		rest = rest[idx+len(`"exit_code"`):]
	}
	if dropped == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d command result(s) dropped)", dropped)
}

// ContextBudget is the context budget of one chat turn: the character budget of
// the outgoing conversation plus the per-tool output budgets. The caller
// resolves it from the chat config / deployment env and hands it to the tool
// loop; a caller without one (a unit test, a non-coder bot) keeps the
// compiled-in defaults.
type ContextBudget struct {
	BudgetChars int              `json:"budget_chars"`
	Limits      ToolResultLimits `json:"tool_result_max_chars"`
}

// IsZero reports whether the budget was never resolved.
func (b ContextBudget) IsZero() bool {
	return b.BudgetChars <= 0 && b.Limits.IsZero()
}

// WithDefaults fills an unresolved budget with the compiled-in defaults.
func (b ContextBudget) WithDefaults() ContextBudget {
	if b.BudgetChars <= 0 {
		b.BudgetChars = DefaultContextBudgetChars
	}
	if b.Limits.IsZero() {
		b.Limits = ResolveToolResultLimits(nil)
	}
	return b
}

// Metadata renders the budget as JSON-friendly state.
func (b ContextBudget) Metadata() map[string]interface{} {
	resolved := b.WithDefaults()
	return map[string]interface{}{
		"budget_chars":          resolved.BudgetChars,
		"tool_result_max_chars": resolved.Limits.Metadata(),
	}
}

// ResolveContextBudget resolves the context budget of a chat turn from its
// shared config and the deployment env.
func ResolveContextBudget(config map[string]interface{}) ContextBudget {
	return ContextBudget{
		BudgetChars: ResolveContextBudgetChars(config),
		Limits:      ResolveToolResultLimits(config),
	}
}

// ContextCompactionStats reports what the conversation budget guard did to one
// outgoing request, so the eviction is observable instead of silent.
type ContextCompactionStats struct {
	MessagesBefore int `json:"messages_before"`
	MessagesAfter  int `json:"messages_after"`
	// TotalCharsBefore / TotalCharsAfter are the summed content size.
	TotalCharsBefore int `json:"total_chars_before"`
	TotalCharsAfter  int `json:"total_chars_after"`
	// EvictedToolResults / EvictedNarration / EvictedUserMessages count the
	// messages whose content was replaced by a short eviction marker.
	EvictedToolResults  int `json:"evicted_tool_results"`
	EvictedNarration    int `json:"evicted_narration"`
	EvictedUserMessages int `json:"evicted_user_messages"`
	// DroppedMessages counts the whole old messages removed as a last resort.
	DroppedMessages int `json:"dropped_messages"`
}

// mergeContextCompactionStats folds a second compaction pass into the first, so
// a round that had to be squeezed twice (ci#822 r46: once with the notice
// reserved, once for real) still reports the totals of the whole round.
func mergeContextCompactionStats(first ContextCompactionStats, second ContextCompactionStats) ContextCompactionStats {
	merged := first
	if second.MessagesBefore > 0 {
		merged.MessagesBefore = second.MessagesBefore
	}
	merged.MessagesAfter = second.MessagesAfter
	merged.TotalCharsBefore = second.TotalCharsBefore
	merged.TotalCharsAfter = second.TotalCharsAfter
	merged.EvictedToolResults += second.EvictedToolResults
	merged.EvictedNarration += second.EvictedNarration
	merged.EvictedUserMessages += second.EvictedUserMessages
	merged.DroppedMessages += second.DroppedMessages
	return merged
}

// ContextUsageReport is the token/usage report of one outgoing request
// (ci#680). It is recorded per chat, surfaced as `context_budget` message
// metadata and returned by the `coder_context` tool, so an agent can
// self-throttle before the provider window is exhausted (ci#654).
type ContextUsageReport struct {
	ChatUUID string `json:"chat_uuid,omitempty"`
	// BudgetChars is the character budget of the conversation and UsedChars what
	// the outgoing request actually carries.
	BudgetChars int `json:"budget_chars"`
	UsedChars   int `json:"used_chars"`
	// UsedPercent is UsedChars/BudgetChars in percent (0 when unset).
	UsedPercent int `json:"used_percent"`
	// EstimatedTokens / AssumedWindowTokens put the usage in model terms.
	EstimatedTokens     int `json:"estimated_tokens"`
	AssumedWindowTokens int `json:"assumed_window_tokens"`
	// Messages is the message count of the request, ToolResultMessages how many
	// of them are tool results.
	Messages           int `json:"messages"`
	ToolResultMessages int `json:"tool_result_messages"`
	// TotalToolResultChars / LargestToolResultChars quantify the tool output the
	// request carries; LargestToolResult names the tool of the biggest one.
	TotalToolResultChars   int    `json:"total_tool_result_chars"`
	LargestToolResultChars int    `json:"largest_tool_result_chars"`
	LargestToolResult      string `json:"largest_tool_result,omitempty"`
	// TruncatedToolResults is how many results this turn's per-tool budget cut,
	// and the Evicted*/Dropped* counters what the budget guard had to squeeze.
	TruncatedToolResults int `json:"truncated_tool_results"`
	EvictedToolResults   int `json:"evicted_tool_results"`
	EvictedNarration     int `json:"evicted_narration"`
	EvictedUserMessages  int `json:"evicted_user_messages"`
	DroppedMessages      int `json:"dropped_messages"`
	// Limits is the effective per-tool output budget of the chat.
	Limits ToolResultLimits `json:"-"`
	// UpdatedAt is when the report was recorded.
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Metadata renders the report as JSON-friendly state (the `context_budget`
// message metadata and the `coder_context` tool payload).
func (r ContextUsageReport) Metadata() map[string]interface{} {
	meta := map[string]interface{}{
		"budget_chars":              r.BudgetChars,
		"used_chars":                r.UsedChars,
		"used_percent":              r.UsedPercent,
		"estimated_tokens":          r.EstimatedTokens,
		"assumed_window_tokens":     r.AssumedWindowTokens,
		"messages":                  r.Messages,
		"tool_result_messages":      r.ToolResultMessages,
		"total_tool_result_chars":   r.TotalToolResultChars,
		"largest_tool_result_chars": r.LargestToolResultChars,
		"truncated_tool_results":    r.TruncatedToolResults,
		"evicted_tool_results":      r.EvictedToolResults,
		"evicted_narration":         r.EvictedNarration,
		"evicted_user_messages":     r.EvictedUserMessages,
		"dropped_messages":          r.DroppedMessages,
		"tool_result_max_chars":     r.Limits.Metadata(),
	}
	if r.LargestToolResult != "" {
		meta["largest_tool_result"] = r.LargestToolResult
	}
	if r.UpdatedAt != "" {
		meta["updated_at"] = r.UpdatedAt
	}
	return meta
}

// BuildContextUsageReport measures an outgoing request and folds in what the
// guards did to it.
func BuildContextUsageReport(messages []map[string]interface{}, budgetChars int, stats ContextCompactionStats, limits ToolResultLimits, truncatedToolResults int) ContextUsageReport {
	used := messagesCharCount(messages)
	report := ContextUsageReport{
		BudgetChars:          budgetChars,
		UsedChars:            used,
		EstimatedTokens:      EstimateTokensForChars(used),
		AssumedWindowTokens:  DefaultModelContextWindowTokens,
		Messages:             len(messages),
		TruncatedToolResults: truncatedToolResults,
		EvictedToolResults:   stats.EvictedToolResults,
		EvictedNarration:     stats.EvictedNarration,
		EvictedUserMessages:  stats.EvictedUserMessages,
		DroppedMessages:      stats.DroppedMessages,
		Limits:               limits,
		UpdatedAt:            time.Now().UTC().Format(time.RFC3339),
	}
	if budgetChars > 0 {
		report.UsedPercent = used * 100 / budgetChars
	}
	names := toolNamesByCallID(messages)
	for _, message := range messages {
		if roleOf(message) != "tool" {
			continue
		}
		chars := messageCharCount(message)
		report.ToolResultMessages++
		report.TotalToolResultChars += chars
		if chars > report.LargestToolResultChars {
			report.LargestToolResultChars = chars
			report.LargestToolResult = names[toolCallIDOf(message)]
		}
	}
	return report
}

// contextUsageRegistry holds the last usage report of every chat this process
// served, so the `coder_context` tool can answer with the state of the chat the
// agent is actually in.
var contextUsageRegistry = struct {
	mu     sync.Mutex
	byChat map[string]ContextUsageReport
}{
	byChat: map[string]ContextUsageReport{},
}

// RecordContextUsage stores the latest usage report of a chat. An empty chat id
// is a no-op.
func RecordContextUsage(chatUUID string, report ContextUsageReport) {
	chatUUID = strings.TrimSpace(chatUUID)
	if chatUUID == "" {
		return
	}
	contextUsageRegistry.mu.Lock()
	defer contextUsageRegistry.mu.Unlock()
	contextUsageRegistry.byChat[chatUUID] = report
}

// ContextUsageFor returns the last recorded usage report of a chat.
func ContextUsageFor(chatUUID string) (ContextUsageReport, bool) {
	chatUUID = strings.TrimSpace(chatUUID)
	if chatUUID == "" {
		return ContextUsageReport{}, false
	}
	contextUsageRegistry.mu.Lock()
	defer contextUsageRegistry.mu.Unlock()
	report, ok := contextUsageRegistry.byChat[chatUUID]
	return report, ok
}

// resetContextUsageRegistry clears the registry. It exists for tests, which
// must not leak one chat's report into the next test.
func resetContextUsageRegistry() {
	contextUsageRegistry.mu.Lock()
	defer contextUsageRegistry.mu.Unlock()
	contextUsageRegistry.byChat = map[string]ContextUsageReport{}
}

// contextUsageLookup is what the `coder_context` tool calls: the recorded report
// of a chat, or the effective budgets alone when the chat is not identified (or
// has not produced a request yet), which is still enough to self-throttle.
func contextUsageLookup(chatUUID string) (map[string]interface{}, bool) {
	if report, ok := ContextUsageFor(chatUUID); ok {
		return report.Metadata(), true
	}
	report := ContextUsageReport{
		BudgetChars:         ResolveContextBudgetChars(nil),
		AssumedWindowTokens: DefaultModelContextWindowTokens,
		Limits:              ResolveToolResultLimits(nil),
	}
	return report.Metadata(), false
}

// compactMessagesToContextBudget squeezes an outgoing conversation under
// budgetChars (ci#680).
//
// It never touches the leading system messages (an injected compaction summary
// survives intact) nor the very last message, which is the turn the model must
// answer. Older tool results and intermediate narration are replaced by a short
// eviction marker, oldest first, so the newest rounds survive longest; a huge
// non-newest user message is truncated; and as a last resort whole old messages
// are dropped (never a user message - a dropped task is worse than a long one).
// The tool-call/tool-result pairing is repaired afterwards, so the request it
// produces is one a provider accepts.
//
// The most recent tool-call round (the assistant message declaring the newest
// calls and every result of that round) is protected as well (ci#728): a turn
// once re-issued the exact same call dozens of times because the guard evicted
// the result of the call it had just made and the eviction marker asked it to
// re-issue that call.
//
// It returns the message list, what it did and whether anything changed; when
// nothing changed the original slice is returned.
func compactMessagesToContextBudget(messages []map[string]interface{}, budgetChars int) ([]map[string]interface{}, ContextCompactionStats, bool) {
	return compactMessagesToContextBudgetReserving(messages, budgetChars, 0)
}

// compactMessagesToContextBudgetReserving is compactMessagesToContextBudget with
// headroom reserved for text that is appended to the request *after* the guard
// ran.
//
// The self-throttling notice is merged into the leading system message, which
// the guard never rewrites (both passes skip it), so text added there can no
// longer be compensated by a later compaction. ci#822 r46: a round whose
// pre-notice total measured as fitting (59889 of 60000 chars) was sent at 60190
// chars once the notice was merged in. Reserving the notice's length before the
// compaction is what keeps every outgoing request under the budget.
func compactMessagesToContextBudgetReserving(messages []map[string]interface{}, budgetChars int, reserveChars int) ([]map[string]interface{}, ContextCompactionStats, bool) {
	stats := ContextCompactionStats{
		MessagesBefore:   len(messages),
		MessagesAfter:    len(messages),
		TotalCharsBefore: messagesCharCount(messages),
	}
	stats.TotalCharsAfter = stats.TotalCharsBefore
	effectiveBudget := budgetChars
	if reserveChars > 0 {
		effectiveBudget = budgetChars - reserveChars
		if effectiveBudget < 0 {
			effectiveBudget = 0
		}
	}
	if effectiveBudget <= 0 || stats.TotalCharsBefore <= effectiveBudget {
		return messages, stats, false
	}

	target := effectiveBudget * contextBudgetTargetRatioPercent / 100
	if target < 1 {
		target = effectiveBudget
	}

	out := make([]map[string]interface{}, len(messages))
	copy(out, messages)
	toolNames := toolNamesByCallID(out)

	leadingSystem := leadingSystemMessageCount(out)
	protectedTailStart := len(out) - contextBudgetProtectedTailMessages
	if protectedTailStart < leadingSystem {
		protectedTailStart = leadingSystem
	}

	// ci#728: the most recent tool-call round is never evicted or dropped, no
	// matter how far back it sits. A turn once re-issued the exact same call
	// dozens of times (110 calls, 18 identical reads) because the guard evicted
	// the result of the call it had just made: the model saw its own call in the
	// history with an eviction marker instead of the output, and "re-issued" it.
	newestRound := newestToolCallRoundIndices(out)

	running := stats.TotalCharsBefore

	// Pass 1: evict the *content* of the oldest non-protected messages, oldest
	// first - so the newest turns of the model survive longest. Neither the very
	// last message (the turn the model must answer) nor the newest tool-call
	// round (ci#728) is ever evicted.
	lastIndex := len(out) - 1
	for i := leadingSystem; i < lastIndex && running > target; i++ {
		if i != lastIndex && newestRound[i] {
			continue
		}
		before := messageCharCount(out[i])
		stubbed, ok := stubMessageForContextBudget(out[i], toolNames, i == lastIndex, newestRound[i])
		if !ok {
			continue
		}
		after := messageCharCount(stubbed)
		if after >= before {
			continue
		}
		switch roleOf(out[i]) {
		case "tool":
			stats.EvictedToolResults++
		case "user":
			stats.EvictedUserMessages++
		case "assistant":
			stats.EvictedNarration++
		}
		out[i] = stubbed
		running -= before - after
	}

	// Pass 2: drop whole old messages, oldest first, keeping the leading system
	// messages, the protected tail, the newest tool-call round (ci#728) and
	// every user message (a dropped task is worse than a long task).
	if running > target {
		kept := make([]map[string]interface{}, 0, len(out))
		for i, message := range out {
			if running > target && i >= leadingSystem && i < protectedTailStart && i != lastIndex && !newestRound[i] && roleOf(message) != "user" {
				running -= messageCharCount(message)
				stats.DroppedMessages++
				continue
			}
			kept = append(kept, message)
		}
		out = kept
	}

	repaired, repairedChanged := repairToolCallPairing(out)
	if repairedChanged {
		out = repaired
	}
	if !hasConversationalTurn(out) {
		return messages, stats, false
	}

	stats.MessagesAfter = len(out)
	stats.TotalCharsAfter = messagesCharCount(out)
	if stats.MessagesAfter == stats.MessagesBefore && stats.TotalCharsAfter == stats.TotalCharsBefore {
		return messages, stats, false
	}
	logging.Debugf(
		"Context budget guard (ci#680): %d -> %d chars, %d -> %d messages (evicted tool results=%d narration=%d user=%d dropped=%d)",
		stats.TotalCharsBefore, stats.TotalCharsAfter, stats.MessagesBefore, stats.MessagesAfter,
		stats.EvictedToolResults, stats.EvictedNarration, stats.EvictedUserMessages, stats.DroppedMessages,
	)
	return out, stats, true
}

// stubMessageForContextBudget replaces one message's content with a short
// eviction marker. It reports false for a message that must not be touched (a
// system message, the newest message, a message of the newest tool-call round,
// an assistant tool-call shell, a small message).
func stubMessageForContextBudget(message map[string]interface{}, toolNames map[string]string, isLast bool, isNewestRound bool) (map[string]interface{}, bool) {
	role := roleOf(message)
	content, _ := message["content"].(string)
	// ci#728: the newest tool-call round is never rewritten. Replacing its
	// result with an eviction marker makes the model believe the call it just
	// made never ran, and it re-issues it - the observed 110-call loop.
	if isNewestRound {
		return nil, false
	}
	if strings.TrimSpace(content) == "" {
		return nil, false
	}
	switch role {
	case "tool":
		name := toolNames[toolCallIDOf(message)]
		if name == "" {
			name = "tool"
		}
		return cloneMessageWithContent(message, fmt.Sprintf(
			"[tool output evicted from the context budget: %s already ran and returned %d chars. "+
				"Do NOT re-issue this call: the result is not available any more. Read a narrower "+
				"range of the same target instead (coder_read: offset/limit, coder_list: depth/entry "+
				"cap, coder_grep: include/paths), or redirect the output to a file and read the ranges "+
				"you need.]",
			name, len(content),
		)), true
	case "user":
		// The newest user message is the task itself - never evict it, and leave
		// a normal-sized earlier message intact.
		if isLast || len(content) <= contextBudgetHugeMessageChars {
			return nil, false
		}
		return cloneMessageWithContent(message, fmt.Sprintf(
			"[earlier user message truncated to fit the context budget (%d chars, first %d kept):\n%s]",
			len(content), contextBudgetUserMessageKeepChars, headOfString(content, contextBudgetUserMessageKeepChars),
		)), true
	case "assistant":
		// An assistant tool-call shell carries the calls the transcript answers;
		// only evict pure intermediate narration.
		if isLast || len(toolCallIDsOf(message)) > 0 {
			return nil, false
		}
		return cloneMessageWithContent(message, fmt.Sprintf(
			"[earlier assistant narration evicted from the context budget (%d chars)]", len(content),
		)), true
	}
	return nil, false
}

// contextBudgetNotice is the one-line self-throttling hint merged into the
// system prompt once the conversation approaches its budget.
func contextBudgetNotice(report ContextUsageReport) string {
	return fmt.Sprintf(
		"[context budget: %d of %d chars used (~%d tokens, %d%%); %d tool result(s) were truncated and %d "+
			"evicted to fit. Prefer targeted coder_read ranges (offset/limit), shallow coder_list "+
			"(depth/entry cap) and filtered coder_bash output over large dumps; call coder_context for "+
			"the current numbers.]",
		report.UsedChars, report.BudgetChars, report.EstimatedTokens, report.UsedPercent,
		report.TruncatedToolResults, report.EvictedToolResults,
	)
}

// contextBudgetNoticeReserveChars is the headroom reserved in the compaction
// budget for the self-throttling notice (plus the blank line separating it from
// the system prompt), so merging the notice in cannot push a request over the
// budget the guard exists to enforce (ci#822 r46).
const contextBudgetNoticeReserveChars = 400

// contextBudgetNoticeFor returns the notice a given message list would get, or
// "" when it stays below the threshold. It is the exact text
// injectContextBudgetNotice merges in, so the compaction can reserve its length
// before the guard runs.
func contextBudgetNoticeFor(messages []map[string]interface{}, report ContextUsageReport) string {
	if report.BudgetChars <= 0 || report.UsedPercent < contextBudgetNoticeThresholdPercent {
		return ""
	}
	if existing := leadingSystemNoticeText(messages); existing != "" {
		return existing
	}
	return contextBudgetNotice(report)
}

// leadingSystemNoticeText returns the budget notice already merged into the
// leading system message, or "" when there is none.
func leadingSystemNoticeText(messages []map[string]interface{}) string {
	for _, message := range messages {
		if roleOf(message) != "system" {
			continue
		}
		content, _ := message["content"].(string)
		for _, paragraph := range strings.Split(content, "\n\n") {
			trimmed := strings.TrimSpace(paragraph)
			if strings.HasPrefix(trimmed, "[context budget:") {
				return trimmed
			}
		}
		return ""
	}
	return ""
}

// injectContextBudgetNotice merges the self-throttling notice into the leading
// system message (never adding a mid-conversation system message, which some
// backends reject). It is a no-op below the threshold, and idempotent: a notice
// of an earlier round is replaced instead of stacked.
func injectContextBudgetNotice(messages []map[string]interface{}, report ContextUsageReport) []map[string]interface{} {
	if report.BudgetChars <= 0 || report.UsedPercent < contextBudgetNoticeThresholdPercent {
		return messages
	}
	notice := contextBudgetNotice(report)
	stripNotice := func(content string) string {
		paragraphs := strings.Split(content, "\n\n")
		kept := paragraphs[:0:0]
		for _, paragraph := range paragraphs {
			if strings.HasPrefix(strings.TrimSpace(paragraph), "[context budget:") {
				continue
			}
			kept = append(kept, paragraph)
		}
		return strings.TrimRight(strings.Join(kept, "\n\n"), "\n")
	}
	if len(messages) > 0 && roleOf(messages[0]) == "system" {
		content, _ := messages[0]["content"].(string)
		out := make([]map[string]interface{}, len(messages))
		copy(out, messages)
		base := stripNotice(content)
		merged := notice
		if strings.TrimSpace(base) != "" {
			merged = base + "\n\n" + notice
		}
		out[0] = cloneMessageWithContent(messages[0], merged)
		return out
	}
	return append([]map[string]interface{}{{"role": "system", "content": notice}}, messages...)
}

// toolNamesByCallID maps every declared tool-call id to its tool name, so an
// eviction marker can name the tool whose output was dropped.
func toolNamesByCallID(messages []map[string]interface{}) map[string]string {
	names := map[string]string{}
	for _, message := range messages {
		if roleOf(message) != "assistant" {
			continue
		}
		for _, call := range toolCallsOf(message) {
			id, _ := call["id"].(string)
			if strings.TrimSpace(id) == "" {
				continue
			}
			name := ""
			if function, ok := call["function"].(map[string]interface{}); ok {
				name, _ = function["name"].(string)
			}
			if name == "" {
				name, _ = call["name"].(string)
			}
			if strings.TrimSpace(name) != "" {
				names[strings.TrimSpace(id)] = strings.TrimSpace(name)
			}
		}
	}
	return names
}

// newestToolCallRoundIndices marks the messages of the most recent tool-call
// round: the newest assistant message that declares tool calls plus every
// message answering one of those calls that follows it (ci#728).
//
// Those messages are the model's own last action together with its output, the
// one thing the turn must never rewrite. When the budget guard replaced such a
// result by an eviction marker telling the model to "re-issue the call", the
// turn re-issued the identical call over and over (observed: 110 calls, 18
// identical coder_read in one rundown). A message list without a tool call - a
// plain chat turn - returns an empty set.
func newestToolCallRoundIndices(messages []map[string]interface{}) map[int]bool {
	protected := map[int]bool{}
	roundStart := -1
	for i, message := range messages {
		if roleOf(message) == "assistant" && len(toolCallsOf(message)) > 0 {
			roundStart = i
		}
	}
	if roundStart < 0 {
		return protected
	}
	callIDs := map[string]struct{}{}
	for _, call := range toolCallsOf(messages[roundStart]) {
		if id := toolCallDeclaredIDOf(call); id != "" {
			callIDs[id] = struct{}{}
		}
	}
	protected[roundStart] = true
	for i := roundStart + 1; i < len(messages); i++ {
		if _, ok := callIDs[toolCallIDOf(messages[i])]; !ok {
			// The round is complete once the answers stop; anything later is a
			// new turn (or narration), not part of the protected round.
			break
		}
		protected[i] = true
	}
	return protected
}

// toolCallDeclaredIDOf returns the id an assistant message declares for one of
// its tool calls, whether the call carries the OpenAI `id` field or the
// `tool_call_id` shape.
func toolCallDeclaredIDOf(call map[string]interface{}) string {
	for _, key := range []string{"id", "tool_call_id"} {
		if id, ok := call[key].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// toolCallsOf returns an assistant message's tool calls in whatever shape the
// loop or a decoded request carries them.
func toolCallsOf(message map[string]interface{}) []map[string]interface{} {
	raw, ok := message["tool_calls"]
	if !ok {
		return nil
	}
	switch calls := raw.(type) {
	case []map[string]interface{}:
		return calls
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(calls))
		for _, entry := range calls {
			if call, ok := entry.(map[string]interface{}); ok {
				out = append(out, call)
			}
		}
		return out
	}
	return nil
}

// roleOf returns a message's role.
func roleOf(message map[string]interface{}) string {
	role, _ := message["role"].(string)
	return strings.TrimSpace(role)
}

// cloneMessageWithContent copies a message and replaces its content.
func cloneMessageWithContent(message map[string]interface{}, content string) map[string]interface{} {
	clone := make(map[string]interface{}, len(message))
	for key, value := range message {
		clone[key] = value
	}
	clone["content"] = content
	return clone
}

// messagesCharCount sums the content size of a message list, counting the tool
// calls an assistant message carries (their arguments are context too).
func messagesCharCount(messages []map[string]interface{}) int {
	total := 0
	for _, message := range messages {
		total += messageCharCount(message)
	}
	return total
}

// messageCharCount measures one message: its content plus its declared tool-call
// names and arguments.
func messageCharCount(message map[string]interface{}) int {
	total := len(contentStringOf(message))
	for _, call := range toolCallsOf(message) {
		if function, ok := call["function"].(map[string]interface{}); ok {
			if name, ok := function["name"].(string); ok {
				total += len(name)
			}
			if arguments, ok := function["arguments"].(string); ok {
				total += len(arguments)
			}
		}
	}
	return total
}

// contentStringOf renders a message's content size-wise: a plain string is its
// own size, a multi-part content list the summed text parts.
func contentStringOf(message map[string]interface{}) string {
	switch content := message["content"].(type) {
	case string:
		return content
	case []map[string]interface{}:
		var b strings.Builder
		for _, part := range content {
			if text, ok := part["text"].(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	case []interface{}:
		var b strings.Builder
		for _, entry := range content {
			if part, ok := entry.(map[string]interface{}); ok {
				if text, ok := part["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	}
	return ""
}

// positiveIntFromValue reads a positive integer config value (int/int64/float
// or a numeric string), reporting false for anything absent or not positive.
func positiveIntFromValue(raw interface{}) (int, bool) {
	switch typed := raw.(type) {
	case int:
		if typed > 0 {
			return typed, true
		}
	case int64:
		if typed > 0 {
			return int(typed), true
		}
	case float64:
		if typed > 0 {
			return int(typed), true
		}
	case float32:
		if typed > 0 {
			return int(typed), true
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil || parsed <= 0 {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// TruncateToolResultForTool exposes the per-tool output budget to a caller
// outside the tool loop (eg a coding integration that wants to pre-truncate a
// result it knows is huge). It reports whether the result was cut.
func TruncateToolResultForTool(result, toolName string, limits ToolResultLimits) (string, bool) {
	return truncateToolResultForTool(result, toolName, limits)
}

// CompactMessagesToContextBudget exposes the conversation budget guard to a
// caller outside the tool loop (eg an integration that wants to pre-shrink a
// history it assembled itself).
func CompactMessagesToContextBudget(messages []map[string]interface{}, budgetChars int) ([]map[string]interface{}, ContextCompactionStats, bool) {
	return compactMessagesToContextBudget(messages, budgetChars)
}

// headOfString returns the first maxChars bytes of s, never splitting a UTF-8
// rune (a cut mid-rune would produce invalid JSON text downstream).
func headOfString(s string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	if len(s) <= maxChars {
		return s
	}
	cut := maxChars
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
