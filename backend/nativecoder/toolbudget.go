package nativecoder

import (
	"encoding/json"
	"strings"
	"time"
)

// Per-chat tool-call budget (ci#709).
//
// A native coder turn used to read the tool-call caps from the chat's shared
// config only. Because that config is a *copy* of the bot runtime's default
// shared config taken when the chat was created, raising the caps on the bot
// never reached a chat that already existed: the old value was frozen into the
// chat, and the turn kept aborting at the old cap while new chats already used
// the raised one.
//
// The caps are now resolved per turn from every declared source, and the chat
// records the budget it was created with (the *snapshot*):
//
//  1. the per-chat snapshot recorded at creation — a floor, so a chat keeps its
//     budget across a re-attach and a re-provision;
//  2. the caps declared in the chat's shared config (an explicit per-chat
//     update);
//  3. the caps declared in the bot's *current* runtime shared config, so
//     raising the caps on a bot takes effect on chats that already exist;
//  4. the compiled-in default.
//
// The effective cap is the maximum of those, so a declared raise always applies
// and a declared budget is never silently lost. The snapshot lives in the
// chat's shared config, so it survives re-attach and re-provision like the rest
// of the chat configuration.

const (
	// ToolCallBudgetConfigKey is the shared-config block holding a chat's
	// tool-call budget snapshot.
	ToolCallBudgetConfigKey = "tool_call_budget"
	// ToolCallMaxTotalConfigKey is the shared-config key of the total tool-call
	// cap (per chat or per bot runtime).
	ToolCallMaxTotalConfigKey = "tool_call_max_total"
	// ToolCallMaxFailedConfigKey is the shared-config key of the failed
	// tool-call cap (per chat or per bot runtime).
	ToolCallMaxFailedConfigKey = "tool_call_max_failed"
	// ToolCallBudgetSnapshotVersion versions the snapshot block so a later
	// change to its shape stays readable.
	ToolCallBudgetSnapshotVersion = 1

	// DefaultToolCallMaxTotal / DefaultToolCallMaxFailed are the compiled-in
	// caps used when neither the chat nor the bot declares one. They are a
	// fleet-wide floor: the effective cap is the maximum of the chat snapshot,
	// the chat/bot config and this default, so raising them lifts chats that
	// still carry the old 100-call snapshot (ci#709) while an explicitly
	// declared larger budget is never lost.
	DefaultToolCallMaxTotal  = 500
	DefaultToolCallMaxFailed = 20
)

// Tool-call budget origins, reported as structured state.
const (
	ToolCallBudgetSourceChatSnapshot = "chat_snapshot"
	ToolCallBudgetSourceChatConfig   = "chat_config"
	ToolCallBudgetSourceBotConfig    = "bot_config"
	ToolCallBudgetSourceDefault      = "default"
)

// ToolCallBudget is the effective tool-call budget of one chat turn: the caps
// the tool loop enforces, where they came from, and what the chat snapshotted
// when it was created.
type ToolCallBudget struct {
	MaxTotal  int `json:"max_total"`
	MaxFailed int `json:"max_failed"`
	// Source names the origin of the effective caps: the per-chat snapshot, the
	// chat's own config, the bot's current config or the compiled-in default.
	Source string `json:"source"`
	// SnapshotMaxTotal / SnapshotMaxFailed / SnapshotAt describe the per-chat
	// snapshot recorded at chat creation (zero/empty when the chat carries
	// none, e.g. a chat created before the snapshot existed).
	SnapshotMaxTotal  int    `json:"snapshot_max_total,omitempty"`
	SnapshotMaxFailed int    `json:"snapshot_max_failed,omitempty"`
	SnapshotAt        string `json:"snapshot_at,omitempty"`
}

// Metadata renders the budget as JSON-friendly state, so a consumer no longer
// has to parse an error string to learn the caps.
func (b ToolCallBudget) Metadata() map[string]interface{} {
	meta := map[string]interface{}{
		"max_total":  b.MaxTotal,
		"max_failed": b.MaxFailed,
		"source":     b.Source,
	}
	if b.SnapshotMaxTotal > 0 {
		meta["snapshot_max_total"] = b.SnapshotMaxTotal
	}
	if b.SnapshotMaxFailed > 0 {
		meta["snapshot_max_failed"] = b.SnapshotMaxFailed
	}
	if b.SnapshotAt != "" {
		meta["snapshot_at"] = b.SnapshotAt
	}
	return meta
}

// ToolCallBudgetUsage is the consumed/remaining counter state of one turn. It
// is surfaced as structured metadata so the caps and the budget left in a chat
// are inspectable without reading the abort error.
type ToolCallBudgetUsage struct {
	ConsumedTotal    int  `json:"consumed_total"`
	RemainingTotal   int  `json:"remaining_total"`
	ConsumedFailed   int  `json:"consumed_failed"`
	RemainingFailed  int  `json:"remaining_failed"`
	ExhaustedTotal   bool `json:"exhausted_total"`
	ExhaustedFailed  bool `json:"exhausted_failed"`
	TransientFailed  int  `json:"transient_failed,omitempty"`
	SandboxCapacityF int  `json:"sandbox_capacity_failed,omitempty"`
	// BudgetExemptTotal counts the calls that were bookkeeping rather than work
	// (`coder_schedule`, ci#724) and therefore did not consume the total
	// budget. It is reported so a self-re-arming turn is readable from the
	// usage metadata instead of only from the completion payload.
	BudgetExemptTotal int `json:"budget_exempt_total,omitempty"`
}

// Usage reports how much of the budget a turn consumed and how much is left.
// budgetExemptTotal counts the bookkeeping calls (`coder_schedule`, ci#724) that
// were deliberately not charged to the total budget.
func (b ToolCallBudget) Usage(consumedTotal, consumedFailed, transientFailed, sandboxCapacityFailed, budgetExemptTotal int) ToolCallBudgetUsage {
	remainingTotal := b.MaxTotal - consumedTotal
	if remainingTotal < 0 {
		remainingTotal = 0
	}
	remainingFailed := b.MaxFailed - consumedFailed
	if remainingFailed < 0 {
		remainingFailed = 0
	}
	return ToolCallBudgetUsage{
		ConsumedTotal:     consumedTotal,
		RemainingTotal:    remainingTotal,
		ConsumedFailed:    consumedFailed,
		RemainingFailed:   remainingFailed,
		ExhaustedTotal:    consumedTotal >= b.MaxTotal,
		ExhaustedFailed:   consumedFailed >= b.MaxFailed,
		TransientFailed:   transientFailed,
		SandboxCapacityF:  sandboxCapacityFailed,
		BudgetExemptTotal: budgetExemptTotal,
	}
}

// Metadata renders the usage as JSON-friendly state.
func (u ToolCallBudgetUsage) Metadata() map[string]interface{} {
	return map[string]interface{}{
		"consumed_total":   u.ConsumedTotal,
		"remaining_total":  u.RemainingTotal,
		"consumed_failed":  u.ConsumedFailed,
		"remaining_failed": u.RemainingFailed,
		"exhausted_total":  u.ExhaustedTotal,
		"exhausted_failed": u.ExhaustedFailed,
		"transient_failed": u.TransientFailed,
		"sandbox_capacity": u.SandboxCapacityF,
		"budget_exempt":    u.BudgetExemptTotal,
		"max_total":        u.ConsumedTotal + u.RemainingTotal,
		"max_failed":       u.ConsumedFailed + u.RemainingFailed,
	}
}

// ResolveToolCallBudget resolves the effective per-turn tool-call caps of a chat
// (ci#709). chatConfig is the chat's shared config, botConfig the bot runtime's
// *current* default shared config (nil when unknown). The effective cap is the
// maximum declared by the chat snapshot, the chat config, the bot config and
// the compiled-in default, so a declared raise always applies and a declared
// budget is never lost.
func ResolveToolCallBudget(chatConfig, botConfig map[string]interface{}) ToolCallBudget {
	budget := ToolCallBudget{
		MaxTotal:  DefaultToolCallMaxTotal,
		MaxFailed: DefaultToolCallMaxFailed,
		Source:    ToolCallBudgetSourceDefault,
	}
	if snapshot, ok := ToolCallBudgetSnapshot(chatConfig); ok {
		budget.SnapshotMaxTotal = snapshot.MaxTotal
		budget.SnapshotMaxFailed = snapshot.MaxFailed
		budget.SnapshotAt = snapshot.SnapshotAt
		if snapshot.MaxTotal >= budget.MaxTotal {
			budget.MaxTotal = snapshot.MaxTotal
			budget.Source = ToolCallBudgetSourceChatSnapshot
		}
		if snapshot.MaxFailed >= budget.MaxFailed {
			budget.MaxFailed = snapshot.MaxFailed
			if budget.Source == ToolCallBudgetSourceDefault {
				budget.Source = ToolCallBudgetSourceChatSnapshot
			}
		}
	}
	if chatTotal, ok := positiveIntFromConfig(chatConfig, ToolCallMaxTotalConfigKey); ok && chatTotal > budget.MaxTotal {
		budget.MaxTotal = chatTotal
		budget.Source = ToolCallBudgetSourceChatConfig
	}
	if chatFailed, ok := positiveIntFromConfig(chatConfig, ToolCallMaxFailedConfigKey); ok && chatFailed > budget.MaxFailed {
		budget.MaxFailed = chatFailed
	}
	if botTotal, ok := positiveIntFromConfig(botConfig, ToolCallMaxTotalConfigKey); ok && botTotal > budget.MaxTotal {
		// The bot's *current* config is read per turn, so raising the caps on a
		// bot takes effect on chats that already exist (the ticket's second
		// regression).
		budget.MaxTotal = botTotal
		budget.Source = ToolCallBudgetSourceBotConfig
	}
	if botFailed, ok := positiveIntFromConfig(botConfig, ToolCallMaxFailedConfigKey); ok && botFailed > budget.MaxFailed {
		budget.MaxFailed = botFailed
		if budget.Source != ToolCallBudgetSourceBotConfig {
			budget.Source = ToolCallBudgetSourceBotConfig
		}
	}
	return budget
}

// ToolCallBudgetSnapshot reads the per-chat tool-call budget snapshot recorded
// at chat creation. ok is false when the config carries none (e.g. a chat
// created before the snapshot existed) or the block is unusable.
func ToolCallBudgetSnapshot(config map[string]interface{}) (ToolCallBudget, bool) {
	if config == nil {
		return ToolCallBudget{}, false
	}
	raw, exists := config[ToolCallBudgetConfigKey]
	if !exists || raw == nil {
		return ToolCallBudget{}, false
	}
	var block map[string]interface{}
	switch typed := raw.(type) {
	case map[string]interface{}:
		block = typed
	default:
		decoded, ok := decodeConfigBlock(raw)
		if !ok {
			return ToolCallBudget{}, false
		}
		block = decoded
	}
	maxTotal, okTotal := positiveIntFromBlock(block, ToolCallMaxTotalConfigKey)
	maxFailed, okFailed := positiveIntFromBlock(block, ToolCallMaxFailedConfigKey)
	if !okTotal && !okFailed {
		return ToolCallBudget{}, false
	}
	snapshotAt, _ := block["snapshot_at"].(string)
	return ToolCallBudget{
		MaxTotal:   maxTotal,
		MaxFailed:  maxFailed,
		Source:     ToolCallBudgetSourceChatSnapshot,
		SnapshotAt: strings.TrimSpace(snapshotAt),
	}, true
}

// HasToolCallBudgetSnapshot reports whether a chat config already carries a
// tool-call budget snapshot.
func HasToolCallBudgetSnapshot(config map[string]interface{}) bool {
	_, ok := ToolCallBudgetSnapshot(config)
	return ok
}

// SnapshotToolCallBudget records the per-chat tool-call budget snapshot in the
// chat's shared config (in place) and returns the same map, so a chat keeps the
// budget it was created with across a re-attach and a re-provision. An existing
// snapshot is never overwritten.
func SnapshotToolCallBudget(config map[string]interface{}, maxTotal, maxFailed int, at time.Time) map[string]interface{} {
	if config == nil {
		return nil
	}
	if HasToolCallBudgetSnapshot(config) {
		return config
	}
	if maxTotal < 1 {
		maxTotal = DefaultToolCallMaxTotal
	}
	if maxFailed < 1 {
		maxFailed = DefaultToolCallMaxFailed
	}
	config[ToolCallBudgetConfigKey] = map[string]interface{}{
		"version":                  ToolCallBudgetSnapshotVersion,
		ToolCallMaxTotalConfigKey:  maxTotal,
		ToolCallMaxFailedConfigKey: maxFailed,
		"snapshot_at":              at.UTC().Format(time.RFC3339),
	}
	return config
}

// WithToolCallBudgetSnapshot records the snapshot of a fresh chat config: the
// caps come from the config itself when declared (an explicit per-chat value or
// the bot runtime defaults merged in at creation) and from the compiled-in
// default otherwise.
func WithToolCallBudgetSnapshot(config map[string]interface{}, at time.Time) map[string]interface{} {
	if config == nil {
		return nil
	}
	if HasToolCallBudgetSnapshot(config) {
		return config
	}
	maxTotal, okTotal := positiveIntFromConfig(config, ToolCallMaxTotalConfigKey)
	if !okTotal {
		maxTotal = DefaultToolCallMaxTotal
	}
	maxFailed, okFailed := positiveIntFromConfig(config, ToolCallMaxFailedConfigKey)
	if !okFailed {
		maxFailed = DefaultToolCallMaxFailed
	}
	return SnapshotToolCallBudget(config, maxTotal, maxFailed, at)
}

// positiveIntFromConfig reads a positive integer config value. ok is false when
// the key is absent, not a number, or not positive.
func positiveIntFromConfig(config map[string]interface{}, key string) (int, bool) {
	if config == nil {
		return 0, false
	}
	return positiveIntFromValue(config[key])
}

func positiveIntFromBlock(block map[string]interface{}, key string) (int, bool) {
	if block == nil {
		return 0, false
	}
	return positiveIntFromValue(block[key])
}

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
		value := 0
		for _, r := range trimmed {
			if r < '0' || r > '9' {
				return 0, false
			}
			value = value*10 + int(r-'0')
			if value > 1_000_000 {
				return 0, false
			}
		}
		if value > 0 {
			return value, true
		}
	}
	return 0, false
}

// decodeConfigBlock decodes a snapshot value that arrived as raw JSON (e.g. a
// json.RawMessage or a []byte) into a map.
func decodeConfigBlock(raw interface{}) (map[string]interface{}, bool) {
	var bytes []byte
	switch typed := raw.(type) {
	case []byte:
		bytes = typed
	case string:
		bytes = []byte(typed)
	default:
		return nil, false
	}
	decoded := map[string]interface{}{}
	if err := json.Unmarshal(bytes, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}
