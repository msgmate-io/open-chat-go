package tools

import (
	"context"

	"backend/suspend"
)

// suspendedTurnRegistry is the process-wide registry of parked tool-confirmation
// turns (ci#1116). It is a package variable so tests can swap in a fresh
// registry instead of touching the shared one.
//
// The registry type itself lives in backend/suspend (a leaf package) rather than
// here: this package already imports backend/api/msgmate, which is where the
// generation parks, so a registry owned by either side would create an import
// cycle.
var suspendedTurnRegistry = suspend.Default()

// suspendedTurns returns the registry the confirmation handlers talk to.
func suspendedTurns() *suspend.Registry { return suspendedTurnRegistry }

// parkSuspendedTurn registers a parked generation so a later approve/reject can
// continue the SAME turn instead of queueing a fresh bot reply. It reports false
// when the record cannot be resumed, in which case the caller keeps the queued
// EnqueueBotReply behaviour.
func parkSuspendedTurn(chatUUID, toolCallID, messageUUID, botUsername, confirmationID string, resume func(ctx context.Context, decision suspend.Decision) error) bool {
	if resume == nil {
		return false
	}
	return suspendedTurns().Park(&suspend.SuspendedTurn{
		ChatUUID:       chatUUID,
		ToolCallID:     toolCallID,
		MessageUUID:    messageUUID,
		BotUsername:    botUsername,
		ConfirmationID: confirmationID,
		Resume:         resume,
	})
}

// resumeParkedTurn delivers a confirmation decision into the parked generation.
// resumed reports whether a live suspension was found and resumed; false means
// the turn is unknown or expired, so the handler must fall back to the queued
// continuation (resumed:false on the HTTP response).
func resumeParkedTurn(ctx context.Context, chatUUID, toolCallID string, decision suspend.Decision) (resumed bool, err error) {
	return suspendedTurns().ResumeWith(ctx, chatUUID, toolCallID, decision)
}

// suspendedTurnParked reports whether the given tool call is currently parked and
// waiting for a confirmation decision. The interaction status path uses it to
// report "waiting for confirmation" on the existing turn.
func suspendedTurnParked(chatUUID, toolCallID string) bool {
	return suspendedTurns().Parked(chatUUID, toolCallID)
}
