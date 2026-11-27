package workqueue

import (
	"github.com/hibiken/asynq"
)

// This file closes the ci#1195 "silent idle freeze" half that the ci#609 guard
// could not see: a native interaction that reports `state=active` forever
// although nothing is running for it.
//
// asynq keeps a task in its *active* list until the worker that leased it either
// completes it or its lease expires. A worker that disappears mid-turn (restart
// or redeploy, OOM kill, an evicted coding-sandbox pod) leaves the task behind as
// an orphan (TaskInfo.IsOrphaned) which only a later recovery pass re-queues.
// Until that pass runs, the chat's liveness check sees an active task, reports
// the interaction as `active` and the transcript gains neither a turn nor an
// error - nothing an operator or the auto-continue dispatcher can act on.
//
// The classification below is a pure function of the listed tasks, so the
// decision is testable without a queue and can be reused by every liveness
// caller (interaction status, stall guard, dispatch dedupe).

// BotReplyLiveness classifies the reply tasks asynq lists as *active* for one
// chat.
type BotReplyLiveness struct {
	// Active is how many reply tasks asynq lists as active.
	Active int
	// Orphaned is how many of those active tasks no worker is processing.
	Orphaned int
}

// TurnLive reports whether a worker is actually running a turn for the chat.
func (l BotReplyLiveness) TurnLive() bool { return l.Active > l.Orphaned }

// Frozen reports whether the chat's only queue-listed activity is orphaned work:
// asynq says "active", no worker says "running".
func (l BotReplyLiveness) Frozen() bool { return l.Active > 0 && l.Active == l.Orphaned }

// classifyBotReplyLiveness counts the active and orphaned tasks of a listing.
// Non-active listings (a caller that passed the pending queue by mistake) count
// as neither, so a queued reply is never mistaken for live work.
func classifyBotReplyLiveness(tasks []*asynq.TaskInfo) BotReplyLiveness {
	liveness := BotReplyLiveness{}
	for _, task := range tasks {
		if task == nil || task.State != asynq.TaskStateActive {
			continue
		}
		liveness.Active++
		if task.IsOrphaned {
			liveness.Orphaned++
		}
	}
	return liveness
}

// BotReplyLivenessForChat classifies the active reply tasks of a chat.
func BotReplyLivenessForChat(inspector *asynq.Inspector, chatUUID string) BotReplyLiveness {
	if inspector == nil || chatUUID == "" {
		return BotReplyLiveness{}
	}
	return classifyBotReplyLiveness(botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID))
}

// BotReplyActiveOrphaned reports whether every reply task asynq lists as active
// for the chat is orphaned, ie the chat looks `active` only because of dead work
// (ci#1195). It is false for a healthy live turn and for a chat without any
// active task.
func BotReplyActiveOrphaned(inspector *asynq.Inspector, chatUUID string) bool {
	return BotReplyLivenessForChat(inspector, chatUUID).Frozen()
}

// BotReplyWaitingWork reports whether a reply is still waiting for a worker for
// the chat - pending, scheduled or awaiting a retry. Together with
// BotReplyActiveOrphaned it separates "frozen" from "queued behind a dead turn":
// a chat with an orphaned active task plus a pending reply still has real work to
// do, so it must not be reported as stalled.
func BotReplyWaitingWork(inspector *asynq.Inspector, chatUUID string) bool {
	if inspector == nil || chatUUID == "" {
		return false
	}
	if len(botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID)) > 0 {
		return true
	}
	if len(botReplyTasks(inspector, inspector.ListScheduledTasks, chatUUID)) > 0 {
		return true
	}
	return len(botReplyTasks(inspector, inspector.ListRetryTasks, chatUUID)) > 0
}
