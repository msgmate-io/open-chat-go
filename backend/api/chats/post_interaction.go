package chats

import (
	"backend/database"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// Post-interaction actions are the "post the final message for them" hook
// (ci#649): a workflow or trigger attaches a `post_action` / `post_actions`
// entry to an interaction's shared config and, when the interaction finishes,
// the finished reply is posted back to the tracker (a GitHub issue/PR comment).
// opencode runs this itself from its chat backend; a native backend (eg the
// msgmate_coder plan bot, which registers no ChatBackendFunc) has no such hook,
// so the core runs it at the shared interaction-completion choke point.
//
// The executor lives in api/bots (which owns the interaction-dispatch path and
// can reach msgmate for the external-backend check). api/msgmate already imports
// api/chats, so chats cannot import it directly; the executor is registered here
// at init instead of being imported.

// PostInteractionRunner executes an interaction's configured post-actions once
// its final reply is persisted.
type PostInteractionRunner func(db *gorm.DB, queueClient *asynq.Client, queueInspector *asynq.Inspector, chat database.Chat, replyText string)

var postInteractionRunner PostInteractionRunner

// RegisterPostInteractionRunner installs the post-interaction executor. It is
// called once from api/bots' init.
func RegisterPostInteractionRunner(r PostInteractionRunner) {
	postInteractionRunner = r
}

// MaybeRunPostInteractionActions runs the registered post-interaction executor
// for an automated participant's finished reply. It is a no-op when no executor
// is registered (eg a build without the bots package) or no database is
// available; the executor itself decides whether the chat has any post-actions.
func MaybeRunPostInteractionActions(db *gorm.DB, queueClient *asynq.Client, queueInspector *asynq.Inspector, chat database.Chat, replyText string) {
	if postInteractionRunner == nil || db == nil {
		return
	}
	postInteractionRunner(db, queueClient, queueInspector, chat, replyText)
}
