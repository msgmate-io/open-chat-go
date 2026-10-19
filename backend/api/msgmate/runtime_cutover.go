package msgmate

import (
	"os"
	"strings"
)

// ci#584 (migration umbrella ci#585): phased rollout and retirement of the
// opencode-era development bots on ci.msgmate.io.
//
// The native msgmate-coder bots are already in rotation and legacy agent names
// are routed onto them by legacyRuntimeBotAliases (see runtime_selector_routing.go).
// What this file adds is the operational switch that decides *how much* of the
// opencode suite is still offered:
//
//	fallback (default) - native runtimes are offered first, opencode stays in the
//	                     payload as an explicit, flagged fallback so a flip can be
//	                     rolled back instantly without a config change.
//	native             - same routing, but the server records that every bot of
//	                     the flip order is expected to run natively (canary window).
//	retired            - opencode is out of rotation: non-native choices are
//	                     dropped from runtime selectors entirely.
//
// The mode is read from NATIVE_CODER_CUTOVER so ops can move a deployment
// through the rollout (and back) without a code change.
const (
	// RuntimeCutoverEnvKey selects the current migration stage.
	RuntimeCutoverEnvKey = "NATIVE_CODER_CUTOVER"

	// CutoverModeFallback keeps opencode configured as the immediate rollback.
	CutoverModeFallback = "fallback"
	// CutoverModeNative marks the whole suite as natively served (canary window).
	CutoverModeNative = "native"
	// CutoverModeRetired stops offering the opencode bots altogether.
	CutoverModeRetired = "retired"
)

// runtimeCutoverBotOrder is the recorded flip order of the ci#584 rollout. The
// native development lead is last because it orchestrates the other bots.
var runtimeCutoverBotOrder = []string{
	"issue-plan-bot",
	"issue-repl-bot",
	"coding-bot",
	"open-chat-go-ci-admin",
	"open-chat-development-lead",
}

// RuntimeCutoverMode returns the configured migration stage. Unknown or unset
// values fall back to the safe default (keep opencode as the rollback).
func RuntimeCutoverMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(RuntimeCutoverEnvKey))) {
	case CutoverModeNative:
		return CutoverModeNative
	case CutoverModeRetired:
		return CutoverModeRetired
	default:
		return CutoverModeFallback
	}
}

// RuntimeCutoverOffersLegacyFallback reports whether a non-native (opencode)
// runtime may still be offered as a rollback choice.
func RuntimeCutoverOffersLegacyFallback() bool {
	return RuntimeCutoverMode() == CutoverModeFallback
}

// RuntimeCutoverRetired reports whether the opencode bots are retired and must
// no longer be offered or routed to.
func RuntimeCutoverRetired() bool {
	return RuntimeCutoverMode() == CutoverModeRetired
}

// RuntimeCutoverBotOrder returns the recorded per-bot flip order (plan → repl →
// coding → admin → lead). It is a copy so callers cannot mutate the record.
func RuntimeCutoverBotOrder() []string {
	out := make([]string, len(runtimeCutoverBotOrder))
	copy(out, runtimeCutoverBotOrder)
	return out
}

// RuntimeCutoverStageForBot reports the migration stage of one agent name:
// "native" for the msgmate-coder bots, and one of the cutover modes for a
// legacy opencode-era name.
func RuntimeCutoverStageForBot(name string) string {
	if IsNativeRuntimeBot(name) {
		return CutoverModeNative
	}
	if _, ok := NativeRuntimeBotAlias(name); !ok {
		return ""
	}
	return RuntimeCutoverMode()
}

// filterRetiredRuntimeChoices drops every non-native runtime choice once the
// opencode suite is retired, so a stale selector payload cannot route a chat
// back onto a bot that is no longer running. It preserves the caller's order.
func filterRetiredRuntimeChoices(choices []interface{}) []interface{} {
	if !RuntimeCutoverRetired() {
		return choices
	}
	kept := make([]interface{}, 0, len(choices))
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]interface{})
		if !ok {
			continue
		}
		if IsNativeRuntimeBot(actionStringField(choice, "bot")) {
			kept = append(kept, choice)
		}
	}
	return kept
}

// retiredChatBackends are the integration chat backends the native runtime
// replaces. A deploy that leaves one of them configured (or restores the
// opencode integration while retired) must not route a chat through it.
var retiredChatBackends = map[string]bool{
	"opencode": true,
}

// IsRetiredChatBackend reports whether an external chat backend is retired by
// the current cutover stage, ie it must not be used even when a chat config or
// an installed integration still declares it.
func IsRetiredChatBackend(name string) bool {
	if !RuntimeCutoverRetired() {
		return false
	}
	return retiredChatBackends[strings.ToLower(strings.TrimSpace(name))]
}
