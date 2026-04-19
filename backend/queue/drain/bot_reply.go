// Package drain holds the out-of-band workers that retire state left behind by
// queue tasks which stop existing without being consumed (ci#656).
//
// It deliberately depends only on `backend/database` and `backend/workqueue`, so
// it stays independent of the task handlers themselves.
package drain

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"backend/database"
	"backend/workqueue"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// BotReplyFailure describes one failed attempt of a `bot:reply` task, together
// with the retry budget asynq hands to the error handler through the task
// context (ci#656).
type BotReplyFailure struct {
	Err        error
	RetryCount int
	MaxRetry   int
	RetryKnown bool
}

// Terminal reports whether asynq retired the task instead of retrying it. A
// retired task is never consumed again, so the message it belonged to must stop
// rendering as queued: the persisted marker is dropped by the drain below.
//
// Everything else is a retryable failure (or a nil error), where the reply may
// still be consumed and the marker must stay.
func (f BotReplyFailure) Terminal() bool {
	if f.Err == nil {
		return false
	}
	// SkipRetry is archived immediately and RevokeTask marks the task done;
	// both leave the queue for good.
	if errors.Is(f.Err, asynq.SkipRetry) || errors.Is(f.Err, asynq.RevokeTask) {
		return true
	}
	if !f.RetryKnown {
		return false
	}
	// asynq archives a failed task once the retry budget is used up, which is
	// exactly when the attempt count reaches the maximum.
	return f.RetryCount >= f.MaxRetry
}

// botReplyFailureFromContext reads the retry metadata asynq attaches to a task
// context; without it (a call outside a worker) the failure counts as
// retryable, so the marker is kept.
func botReplyFailureFromContext(ctx context.Context, err error) BotReplyFailure {
	failure := BotReplyFailure{Err: err}
	if ctx == nil {
		return failure
	}
	count, countOK := asynq.GetRetryCount(ctx)
	if !countOK {
		return failure
	}
	max, maxOK := asynq.GetMaxRetry(ctx)
	if !maxOK {
		return failure
	}
	failure.RetryCount = count
	failure.MaxRetry = max
	failure.RetryKnown = true
	return failure
}

// BotReplyTerminalDrain returns the asynq error handler that drops the queued
// marker (ci#656) from the source message of a `bot:reply` task which left the
// queue without being consumed.
//
// The marker is written when a reply is queued behind a live turn and is
// otherwise only cleared on consume or by an explicit supersede/cancel, so a
// task asynq retires instead of running (retries exhausted, a SkipRetry failure
// or a revoked task) would leave its message rendering as pending until the
// user happens to send again. The drain closes that path: it runs for every
// failed attempt but acts only on a terminal one, clears the marker of the
// failing task's own message only - a newer reply queued behind the same chat
// keeps its marker - and never fails the task it observes.
func BotReplyTerminalDrain(DB *gorm.DB) asynq.ErrorHandler {
	return asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		if DB == nil || task == nil || task.Type() != workqueue.TypeBotReply {
			return
		}
		if !botReplyFailureFromContext(ctx, err).Terminal() {
			return
		}

		var payload workqueue.BotReplyPayload
		if jsonErr := json.Unmarshal(task.Payload(), &payload); jsonErr != nil {
			log.Printf("ci#656: could not decode the payload of a retired bot reply: %v", jsonErr)
			return
		}
		if payload.MessageUUID == "" {
			return
		}

		var message database.Message
		if findErr := DB.Where("uuid = ?", payload.MessageUUID).First(&message).Error; findErr != nil {
			if !errors.Is(findErr, gorm.ErrRecordNotFound) {
				log.Printf("ci#656: could not load message %s to drop its queued marker: %v", payload.MessageUUID, findErr)
			}
			return
		}
		if !database.IsMessageQueued(message) {
			return
		}

		cleared, clearErr := database.ClearMessageQueued(DB, &message)
		if clearErr != nil {
			log.Printf("ci#656: could not drop the queued marker on message %s: %v", payload.MessageUUID, clearErr)
			return
		}
		if cleared {
			log.Printf("ci#656: message %s is no longer queued: its reply was retired without being consumed (%v)", payload.MessageUUID, err)
		}
	})
}
