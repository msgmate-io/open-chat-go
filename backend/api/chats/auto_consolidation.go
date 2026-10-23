package chats

import (
	"backend/database"

	"gorm.io/gorm"
)

// Auto-consolidation is the native coder's "roll my own ready PRs up" behaviour
// (ci#653). It is defaulted ON for a native msgmate_coder agent through the
// integration's shared-config defaults, but the executor itself lives in
// api/bots (which owns the interaction/dispatch path and can reach the GitHub
// client). api/msgmate already imports api/chats, so chats cannot import api/bots
// directly; the executor is registered here at init instead, exactly like the
// post-interaction hook (ci#649).
//
// It is invoked from the same interaction-completion choke point as the
// auto-continuation loop (ci#537) and the post-interaction actions (ci#649).

// AutoConsolidationRunner runs one auto-consolidation pass for a chat.
type AutoConsolidationRunner func(db *gorm.DB, chat database.Chat)

var autoConsolidationRunner AutoConsolidationRunner

// RegisterAutoConsolidationRunner installs the auto-consolidation executor. It
// is called once from api/bots' init.
func RegisterAutoConsolidationRunner(r AutoConsolidationRunner) {
	autoConsolidationRunner = r
}

// MaybeRunAutoConsolidation runs the registered auto-consolidation executor for
// a chat. It is a no-op when no executor is registered (eg a build without the
// bots package) or no database is available; the executor itself decides whether
// the chat's config enables auto-consolidation (and whether a GitHub client is
// configured at all).
func MaybeRunAutoConsolidation(db *gorm.DB, chat database.Chat) {
	if autoConsolidationRunner == nil || db == nil {
		return
	}
	autoConsolidationRunner(db, chat)
}

// AutoConsolidationRunnerRegistered reports whether an auto-consolidation
// executor is registered, so a caller (or a test) can tell a build without the
// bots package from an enabled feature.
func AutoConsolidationRunnerRegistered() bool {
	return autoConsolidationRunner != nil
}
