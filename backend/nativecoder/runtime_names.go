package nativecoder

// Native runtime names of the msgmate_coder family. Kept here (a copy of the
// registry now owned by backend/api/msgmate/rollout.go) while the coder
// dev-set-up fleet helpers that reference them are migrated into the shared
// coder library.
const (
	NativeBotPlanRuntime   = "msgmate-coder-plan-bot"
	NativeBotReplRuntime   = "msgmate-coder-repl-bot"
	NativeBotReviewRuntime = "msgmate-coder-review-bot"
	NativeBotLeadRuntime   = "msgmate-coder-development-lead"
	NativeBotAdminRuntime  = NativeBotLeadRuntime
)
