package msgmate

import "strings"

// NativeRuntimeBots are the native msgmate-coder runtimes a runtime selector /
// agent chooser may offer. They replace the opencode-era bots (ci#617, ci#621).
const (
	NativeRuntimePlanBot   = "msgmate-coder-plan-bot"
	NativeRuntimeReplBot   = "msgmate-coder-repl-bot"
	NativeRuntimeReviewBot = "msgmate-coder-review-bot"
	NativeRuntimeLeadBot   = "msgmate-coder-development-lead"
	NativeRuntimeAdminBot  = "msgmate-coder-development-lead"
)

// legacyRuntimeBotAliases maps the opencode-era agent names that older selector
// configs still carry onto the native msgmate-coder runtime that replaces them
// (docs/native-coder-parity.md). The map is the single place the flip is
// declared, so retiring a family is a one-line change.
var legacyRuntimeBotAliases = map[string]string{
	"issue-plan-bot":              NativeRuntimePlanBot,
	"issue-repl-bot":              NativeRuntimeReplBot,
	"coding-bot":                  NativeRuntimeReplBot,
	"issue-review-bot":            NativeRuntimeRevwBot,
	"review-bot":                  NativeRuntimeRevwBot,
	"open-chat-development-lead":  NativeRuntimeLeadBot,
	"open-chat-go-ci-admin":       NativeRuntimeAdminBot,
	"opencode":                    NativeRuntimeReplBot,
	"opencode_select_project-bot": NativeRuntimeReplBot,
}

// NativeRuntimeBotAlias returns the native msgmate-coder runtime that replaces a
// legacy (opencode-era) agent name. ok is false when the name is unknown or
// already native, so callers can keep an explicit native choice untouched.
func NativeRuntimeBotAlias(name string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || strings.HasPrefix(key, "msgmate-coder") {
		return "", false
	}
	native, ok := legacyRuntimeBotAliases[key]
	return native, ok
}

// IsNativeRuntimeBot reports whether a bot name is a native msgmate-coder
// runtime.
func IsNativeRuntimeBot(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "msgmate-coder")
}

// RuntimeSelectorChoiceBotName resolves the bot a selector choice routes to: the
// native runtime of the chosen agent, so confirming an older choice that still
// names an opencode-era bot starts the native interaction instead.
func RuntimeSelectorChoiceBotName(choice map[string]interface{}) string {
	if choice == nil {
		return ""
	}
	name := actionStringField(choice, "bot")
	if native, ok := NativeRuntimeBotAlias(name); ok {
		return native
	}
	return name
}

// normalizeRuntimeSelectorPayloadChoices rewrites the choices carried by a
// runtime-selector payload so the native runtimes are offered by default:
// legacy agent names are routed to their native runtime, native choices are
// moved to the front, the first one is flagged recommended, and the original
// name is preserved for audit/rollback. It is idempotent.
func normalizeRuntimeSelectorPayloadChoices(payload map[string]interface{}) {
	if payload == nil {
		return
	}
	choices, ok := payload["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return
	}

	nativeChoices := make([]interface{}, 0, len(choices))
	fallbackChoices := make([]interface{}, 0, len(choices))
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]interface{})
		if !ok {
			fallbackChoices = append(fallbackChoices, rawChoice)
			continue
		}
		bot := actionStringField(choice, "bot")
		if native, ok := NativeRuntimeBotAlias(bot); ok {
			if _, hasLegacy := choice["legacy_bot"]; !hasLegacy && bot != "" {
				choice["legacy_bot"] = bot
			}
			choice["bot"] = native
		}
		choice["runtime"] = "native"
		if bot = actionStringField(choice, "bot"); IsNativeRuntimeBot(bot) {
			nativeChoices = append(nativeChoices, choice)
			continue
		}
		fallbackChoices = append(fallbackChoices, choice)
	}

	ordered := append(nativeChoices, fallbackChoices...)
	for _, rawChoice := range ordered {
		if choice, ok := rawChoice.(map[string]interface{}); ok {
			delete(choice, "recommended")
			delete(choice, "fallback")
		}
	}
	if len(nativeChoices) > 0 {
		if first, ok := ordered[0].(map[string]interface{}); ok {
			first["recommended"] = true
		}
	}
	// opencode (or any other non-native runtime) is only ever a fallback once a
	// native runtime is offered.
	for _, rawChoice := range fallbackChoices {
		if choice, ok := rawChoice.(map[string]interface{}); ok {
			choice["fallback"] = true
		}
	}
	payload["choices"] = ordered
	payload["native_runtime_by_default"] = len(nativeChoices) > 0
	for i, rawChoice := range ordered {
		if choice, ok := rawChoice.(map[string]interface{}); ok {
			choice["index"] = i
		}
	}
}

// actionStringField reads a trimmed string field from a decoded JSON map. It is
// the local counterpart of the action-task helpers used by the chats package.
func actionStringField(data map[string]interface{}, key string) string {
	if data == nil {
		return ""
	}
	value, ok := data[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
