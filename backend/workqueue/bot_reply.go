package workqueue

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"backend/logging"

	"github.com/hibiken/asynq"
)

// defaultBotReplyTimeout bounds a single bot reply turn. It is deliberately
// generous: a native coder turn makes many tool calls (each a provider
// round-trip and often a sandbox operation) and routinely runs for well over
// five minutes, so the previous 5-minute cap cut such turns off mid-work and
// mislabelled them as user cancellations ("I paused this response", ci#642).
// A genuine user stop still aborts the turn immediately through the chat
// interrupt endpoint, so the deadline is only a backstop against a hung turn.
const defaultBotReplyTimeout = 30 * time.Minute

// BotReplyTimeoutEnv overrides the bot reply turn deadline with a Go duration
// (e.g. "45m"); an empty, invalid or non-positive value falls back to the
// default.
const BotReplyTimeoutEnv = "OPEN_CHAT_BOT_REPLY_TIMEOUT"

// BotReplyTimeout resolves the deadline applied to a bot reply task.
func BotReplyTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(BotReplyTimeoutEnv))
	if raw == "" {
		return defaultBotReplyTimeout
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return defaultBotReplyTimeout
	}
	return parsed
}

// BotReplyTaskID returns a stable asynq task ID for the bot reply in a chat.
func BotReplyTaskID(chatUUID string) string {
	return "bot-reply:" + chatUUID
}

// botReplyTaskID returns the asynq task ID for a bot reply. The base ID is used
// for a chat's reply when no turn is running; while a turn is live, further
// messages queue behind it under a unique ID (ci#656).
func botReplyTaskID(chatUUID, messageUUID string) string {
	if strings.TrimSpace(messageUUID) == "" {
		return BotReplyTaskID(chatUUID)
	}
	return BotReplyTaskID(chatUUID) + ":" + messageUUID
}

// botReplyQueuePrefix matches every reply task for a chat: the base ID and the
// unique IDs of replies queued behind a live turn.
func botReplyQueuePrefix(chatUUID string) string {
	return BotReplyTaskID(chatUUID) + ":"
}

// botReplyTasks lists the reply tasks of a chat in the given state.
func botReplyTasks(inspector *asynq.Inspector, list func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error), chatUUID string) []*asynq.TaskInfo {
	if inspector == nil {
		return nil
	}
	base := BotReplyTaskID(chatUUID)
	prefix := botReplyQueuePrefix(chatUUID)
	tasks, err := list(QueueDefault, asynq.PageSize(100))
	if err != nil {
		return nil
	}
	matched := make([]*asynq.TaskInfo, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == base || strings.HasPrefix(task.ID, prefix) {
			matched = append(matched, task)
		}
	}
	return matched
}

// BotReplyIsActive reports whether a turn is live for the chat. A live turn is
// never cancelled by a new message or a continuation (ci#656): the new reply is
// queued behind it instead.
func BotReplyIsActive(inspector *asynq.Inspector, chatUUID string) bool {
	return len(botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID)) > 0
}

// BotReplyHasWork reports whether a reply is queued or running for the chat.
// Unlike BotReplyIsActive it also counts pending, scheduled and retry tasks, so
// liveness checks (dispatch dedupe, stall detection) do not mistake a reply
// waiting for a worker for an idle chat.
func BotReplyHasWork(inspector *asynq.Inspector, chatUUID string) bool {
	if inspector == nil || chatUUID == "" {
		return false
	}
	if len(botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID)) > 0 {
		return true
	}
	if len(botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID)) > 0 {
		return true
	}
	if len(botReplyTasks(inspector, inspector.ListScheduledTasks, chatUUID)) > 0 {
		return true
	}
	return len(botReplyTasks(inspector, inspector.ListRetryTasks, chatUUID)) > 0
}

// BotReplyDeliveryMode selects how a message sent into a chat that is already
// running a turn is delivered (ci#656).
type BotReplyDeliveryMode string

const (
	// BotReplyDeliveryQueued appends the reply behind the live turn: the work
	// already done is preserved and the message is answered once the turn
	// completes. It is the default for a message into a running chat.
	BotReplyDeliveryQueued BotReplyDeliveryMode = "queued"
	// BotReplyDeliveryInterrupt stops the live turn now and answers the message
	// immediately. The partial progress of the stopped turn (files, commits) is
	// already persisted, so nothing is lost.
	BotReplyDeliveryInterrupt BotReplyDeliveryMode = "interrupt"
)

// ParseBotReplyDeliveryMode normalises a client-supplied delivery mode. An
// empty value means the default (queued). It reports false for an unknown mode
// so callers reject it instead of silently guessing.
func ParseBotReplyDeliveryMode(raw string) (BotReplyDeliveryMode, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", string(BotReplyDeliveryQueued), "queue", "task-stack", "task_stack":
		return BotReplyDeliveryQueued, true
	case string(BotReplyDeliveryInterrupt), "immediate", "send_now", "send-now", "now":
		return BotReplyDeliveryInterrupt, true
	default:
		return "", false
	}
}

// BotReplyEnqueueDecision names how a new reply interacts with the reply tasks
// a chat already has. It is a pure function of the requested delivery mode and
// whether a turn is live, so the queue behaviour is testable without a queue.
type BotReplyEnqueueDecision string

const (
	// BotReplyDecisionReplace drops the queued-but-unstarted replies: nothing
	// is running, the newest message wins.
	BotReplyDecisionReplace BotReplyEnqueueDecision = "replace"
	// BotReplyDecisionQueueBehind keeps the live turn and appends the reply
	// behind it.
	BotReplyDecisionQueueBehind BotReplyEnqueueDecision = "queue-behind"
	// BotReplyDecisionInterruptTurn stops the live turn and starts the new one.
	BotReplyDecisionInterruptTurn BotReplyEnqueueDecision = "interrupt-turn"
)

// DecideBotReplyEnqueue maps a delivery mode and the live-turn state to the
// enqueue decision (ci#656).
func DecideBotReplyEnqueue(mode BotReplyDeliveryMode, turnLive bool) BotReplyEnqueueDecision {
	if mode == BotReplyDeliveryInterrupt {
		return BotReplyDecisionInterruptTurn
	}
	if turnLive {
		return BotReplyDecisionQueueBehind
	}
	return BotReplyDecisionReplace
}

// BotReplyQueuedCount reports how many replies are queued behind the chat's
// live turn, ie how many messages the transcript should render as pending
// (ci#656). A turn that is not live cannot have anything queued behind it, so
// the count is 0 then: a single pending reply is just the next turn waiting for
// a worker, not a message that was sent into a running chat.
func BotReplyQueuedCount(inspector *asynq.Inspector, chatUUID string) int {
	if inspector == nil || chatUUID == "" {
		return 0
	}
	if !BotReplyIsActive(inspector, chatUUID) {
		return 0
	}
	base := BotReplyTaskID(chatUUID)
	count := 0
	for _, task := range botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID) {
		if task.ID != base {
			count++
		}
	}
	return count
}

// CancelBotReplyTask stops the in-flight bot reply and removes every queued
// reply for the chat, including replies queued behind a live turn.
func CancelBotReplyTask(inspector *asynq.Inspector, chatUUID string) {
	if inspector == nil || chatUUID == "" {
		return
	}
	tasks := botReplyTasks(inspector, inspector.ListActiveTasks, chatUUID)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListPendingTasks, chatUUID)...)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListScheduledTasks, chatUUID)...)
	tasks = append(tasks, botReplyTasks(inspector, inspector.ListRetryTasks, chatUUID)...)
	for _, task := range tasks {
		_ = inspector.CancelProcessing(task.ID)
		_ = inspector.DeleteTask(QueueDefault, task.ID)
	}
}

// EnqueueBotReply schedules a bot reply with the default delivery mode: a live
// turn is never cancelled, the reply is queued behind it (ci#656).
func EnqueueBotReply(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return EnqueueBotReplyWithDelivery(client, inspector, payload, BotReplyDeliveryQueued, opts...)
}

// EnqueueBotReplyWithDelivery schedules a bot reply honouring an explicit
// delivery mode (ci#656).
//
//   - queued (default): a live turn keeps running and the reply is queued
//     behind it under a unique task ID, so a message sent into a running chat
//     is answered instead of disappearing. When no turn is live, any queued
//     reply for the chat is replaced by the new one.
//   - interrupt: every reply task of the chat (live turn included) is cancelled
//     and the new reply takes the base task ID, so it starts immediately. Work
//     the stopped turn already persisted (files, commits) is untouched.
func EnqueueBotReplyWithDelivery(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, mode BotReplyDeliveryMode, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return enqueueBotReplyWithDelivery(client, inspector, payload, mode, BotReplyIsActive(inspector, payload.ChatUUID), opts...)
}

// enqueueBotReplyWithDelivery is the liveness-injected core of
// EnqueueBotReplyWithDelivery. turnLive is passed in so the queue behaviour is
// testable without a running worker: a task waiting in the queue is *pending*,
// not *active*, so a unit test cannot otherwise reach the queue-behind case.
func enqueueBotReplyWithDelivery(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, mode BotReplyDeliveryMode, turnLive bool, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is required")
	}

	// Idle and interrupt replies take the stable per-chat task ID (the same key
	// the API tests and the dispatch dedupe expect) after every reply task of
	// the chat has been cleared. Only a reply queued behind a live turn needs a
	// per-message ID, because the live turn keeps the base ID: asynq archives a
	// finished/cancelled task under its ID and a re-enqueue with the same ID
	// fails with "task already exists", which would silently block a queued
	// reply.
	taskID := BotReplyTaskID(payload.ChatUUID)
	switch DecideBotReplyEnqueue(mode, turnLive) {
	case BotReplyDecisionQueueBehind:
		// The live turn keeps running: append the reply behind it under its own
		// per-message task ID instead of clobbering the base ID.
		taskID = botReplyTaskID(payload.ChatUUID, payload.MessageUUID)
		if inspector != nil {
			_ = inspector.DeleteTask(QueueDefault, taskID)
		}
	default:
		// interrupt (stop the live turn now) and replace (nothing is running,
		// the newest message wins) both clear every reply task of the chat,
		// which frees the base ID for the new reply.
		CancelBotReplyTask(inspector, payload.ChatUUID)
	}

	task, err := NewBotReplyTask(payload)
	if err != nil {
		return nil, err
	}

	enqueue := func(id string) (*asynq.TaskInfo, error) {
		enqueueOpts := []asynq.Option{
			asynq.Queue(QueueDefault),
			asynq.TaskID(id),
			asynq.MaxRetry(10),
			asynq.Timeout(BotReplyTimeout()),
			asynq.Retention(0),
		}
		enqueueOpts = append(enqueueOpts, opts...)
		return client.Enqueue(task, enqueueOpts...)
	}
	info, err := enqueue(taskID)
	if err != nil {
		// The stable per-chat ID can still be held by an archived/finished task
		// from an earlier turn; asynq then rejects the re-enqueue. Fall back to a
		// unique ID instead of failing the whole reply. A genuine queue error is
		// returned by the second attempt.
		info, err = enqueue(taskID + ":" + strconv.FormatInt(time.Now().UnixNano(), 36))
	}
	return info, err
}

// DecideBotReplyContinuation maps the live-turn state to the enqueue decision
// for a *continuation*: the follow-up bot turn a confirmable action asks for
// after it was executed (or failed) and no parked generation absorbed the
// decision (ci#1142).
//
// A continuation is never queued behind a live turn. The turn it would queue
// behind is precisely the one that did not take the decision: either its
// in-memory decision registry is gone (another instance, a restarted process,
// an expired park) or it is about to end on its park TTL. Queueing behind it
// starves the continuation for the whole park TTL (10 minutes) while the chat
// keeps reporting itself as active - there is a queued reply, so every liveness
// check stays green - and the interaction is reaped as a stall although the
// chat is healthy and merely never got its follow-up turn.
//
// So the continuation always takes the base task ID: an unresponsive parked
// turn is cancelled (its work is already persisted) and the continuation starts
// immediately. turnLive is still a parameter so the invariant is testable for
// both states.
func DecideBotReplyContinuation(turnLive bool) BotReplyEnqueueDecision {
	return BotReplyDecisionInterruptTurn
}

// botReplyContinuationTaskID returns the asynq task ID of a bot-reply
// continuation (ci#1147).
//
// A continuation is derived from the event message of the confirmable action it
// follows, so two continuations of the same chat can never collide and neither
// has to be given the chat's base ID. The ID stays inside the chat's reply
// task prefix, so CancelBotReplyTask, BotReplyHasWork and the queued-count
// helpers keep seeing it.
func botReplyContinuationTaskID(chatUUID, messageUUID string) string {
	if strings.TrimSpace(messageUUID) == "" {
		// No event to key on: fall back to a unique suffix instead of the base
		// ID, which a live turn may already hold.
		return BotReplyTaskID(chatUUID) + ":continuation:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return botReplyTaskID(chatUUID, messageUUID) + ":continuation"
}

// enqueueBotReplyTaskUnderID enqueues one bot-reply task under an explicit task
// ID without touching any other reply task of the chat. It keeps the archived-ID
// fallback of the normal enqueue path: asynq archives a finished task under its
// ID and a re-enqueue with that ID is rejected (ci#747 RC4), so a conflict
// retries once with a unique suffix instead of dropping the turn.
func enqueueBotReplyTaskUnderID(client *asynq.Client, payload BotReplyPayload, taskID string, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	task, err := NewBotReplyTask(payload)
	if err != nil {
		return nil, err
	}
	enqueue := func(id string) (*asynq.TaskInfo, error) {
		enqueueOpts := []asynq.Option{
			asynq.Queue(QueueDefault),
			asynq.TaskID(id),
			asynq.MaxRetry(10),
			asynq.Timeout(BotReplyTimeout()),
			asynq.Retention(0),
		}
		enqueueOpts = append(enqueueOpts, opts...)
		return client.Enqueue(task, enqueueOpts...)
	}
	info, err := enqueue(taskID)
	if err != nil {
		info, err = enqueue(taskID + ":" + strconv.FormatInt(time.Now().UnixNano(), 36))
	}
	return info, err
}

// EnqueueBotReplyContinuation enqueues the follow-up bot turn of a confirmable
// action that no parked generation picked up (ci#1142, ci#1147).
//
// Unlike EnqueueBotReply it never attaches the continuation behind whatever is
// currently running for the chat, so the follow-up turn cannot be starved by a
// parked turn whose decision registry is gone. A nil/failed enqueue is reported
// to the caller instead of being swallowed: the caller must be able to say out
// loud that the chat is left without a bot turn.
func EnqueueBotReplyContinuation(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return enqueueBotReplyContinuation(client, inspector, payload, BotReplyIsActive(inspector, payload.ChatUUID), opts...)
}

// enqueueBotReplyContinuation is the liveness-injected core of
// EnqueueBotReplyContinuation (same reason as enqueueBotReplyWithDelivery: a
// queued task is pending, not active, so a unit test cannot otherwise reach the
// live-turn case).
func enqueueBotReplyContinuation(client *asynq.Client, inspector *asynq.Inspector, payload BotReplyPayload, turnLive bool, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is required")
	}
	decision := DecideBotReplyContinuation(turnLive)
	if decision != BotReplyDecisionInterruptTurn {
		// Unreachable by construction; kept so a future change to the decision
		// cannot silently reintroduce the ci#1142 starvation.
		return nil, fmt.Errorf("continuation for chat %s would be queued behind a live turn (%s); refusing to starve it", payload.ChatUUID, decision)
	}
	taskID := botReplyContinuationTaskID(payload.ChatUUID, payload.MessageUUID)
	// The decision is logged unconditionally: ci#1147 was invisible in the log
	// because nothing said whether a continuation was enqueued, merged onto an
	// existing task, or never asked for.
	logging.Infof(
		"bot-reply continuation (ci#1147): chat %s event %s enqueue_decision=%s turn_live=%v task_id=%s",
		payload.ChatUUID, payload.MessageUUID, decision, turnLive, taskID,
	)

	var info *asynq.TaskInfo
	var err error
	if turnLive {
		// Visible in the log on purpose: this is the ci#1142 shape, and the
		// orphaned turn is about to be cancelled.
		logging.Warnf(
			"bot-reply continuation (ci#1142): chat %s still reports a live turn that did not take the confirmation decision; cancelling it so the continuation starts now",
			payload.ChatUUID,
		)
		// Interrupt takes the chat's base ID, which the orphaned live turn holds:
		// it has to be cleared before the continuation can own it.
		info, err = enqueueBotReplyWithDelivery(client, inspector, payload, BotReplyDeliveryInterrupt, turnLive, opts...)
	} else {
		// Nothing is running, so the continuation is a plain enqueue under its
		// own event-keyed ID. It deliberately does NOT sweep the chat's pending
		// replies: with several confirmable calls in one round a sibling
		// approval's continuation would otherwise be dropped before it ran,
		// which is exactly the ci#1147 shape ("every call decided, no bot turn").
		info, err = enqueueBotReplyTaskUnderID(client, payload, taskID, opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue bot-reply continuation for chat %s: %w", payload.ChatUUID, err)
	}
	if info == nil {
		return nil, fmt.Errorf("bot-reply continuation for chat %s was not enqueued (empty task info)", payload.ChatUUID)
	}
	// ci#1163: the dispatch-side line of the continuation. The decision log above
	// only proves the enqueue was *attempted*. The private-sandbox stalls showed a
	// chat that kept reporting itself active with a queued continuation and no bot
	// turn for the whole reap window, which cannot be told apart from "the worker
	// never consumed the task" without the queue, state and type the task actually
	// landed in.
	logging.Infof(
		"bot-reply continuation (ci#1163): chat %s event %s queued task_id=%s queue=%s type=%s state=%s",
		payload.ChatUUID, payload.MessageUUID, info.ID, info.Queue, info.Type, info.State,
	)
	// ci#1163: a continuation that lands in a queue the embedded worker does not
	// consume is indistinguishable, from the chat's point of view, from one the
	// worker never got to: the chat stays "active" with no bot turn until the
	// harness reaps it. Warn on the mismatch so the next stall names its cause.
	if queue := info.Queue; queue != "" && queue != QueueDefault {
		logging.Warnf(
			"bot-reply continuation (ci#1163): chat %s task %s landed in queue %q instead of %q; the embedded worker does not consume that queue, so the chat would stay active without a bot turn",
			payload.ChatUUID, info.ID, queue, QueueDefault,
		)
	}
	if info.ID != taskID {
		// asynq rejected the event-keyed ID (an archived/finished task still
		// holds it) and the enqueue fell back to a unique ID. Logged because it
		// is the difference between "this continuation" and "a merged one".
		logging.Infof(
			"bot-reply continuation (ci#1147): chat %s event %s skipped_merged_task_id=true (requested %s, queued %s)",
			payload.ChatUUID, payload.MessageUUID, taskID, info.ID,
		)
	}

	// ci#1148: an enqueued continuation is invisible in the chat until a worker
	// consumes it, and the confirmable-action endpoint reported
	// continuation=continuation_queued while the interaction still timed out
	// with state=active and no bot message. Verify the task is actually there
	// so a lost continuation surfaces as a continuation error instead of a
	// silent 720s stall.
	if err := verifyBotReplyContinuationQueued(inspector, payload, info); err != nil {
		return info, err
	}
	return info, nil
}

// verifyBotReplyContinuationQueued re-reads a just-enqueued continuation from
// the queue (ci#1148).
//
// "asynq accepted the enqueue" is not the same as "a worker will run this
// turn": the task can be aliased onto an ID that is already gone, the queue can
// be one this deployment's worker does not poll, or the task can be removed
// between enqueue and pickup. In every one of those cases the chat keeps
// reporting an active interaction while no bot turn ever runs, which is
// indistinguishable from a slow model call and ends in the 720s interaction
// timeout the private sandbox runs hit (11 coder probes, all with
// state=active, messages=4, last_message=false right after an approved
// confirmable action). Reading the task back turns that into a loud, attributable
// failure at the decision point.
//
// A nil inspector is tolerated (callers without one still get a best-effort
// enqueue), matching EnqueueBotReplyContinuation's signature.
func verifyBotReplyContinuationQueued(inspector *asynq.Inspector, payload BotReplyPayload, info *asynq.TaskInfo) error {
	if inspector == nil || info == nil {
		return nil
	}
	queued, err := inspector.GetTaskInfo(QueueDefault, info.ID)
	if err != nil {
		logging.Errorf(
			"bot-reply continuation (ci#1148): chat %s event %s was enqueued as task %s but cannot be read back (%v); the interaction is left without a bot turn",
			payload.ChatUUID, payload.MessageUUID, info.ID, err,
		)
		return fmt.Errorf(
			"bot-reply continuation for chat %s enqueued as %s but is not present in queue %s: %w",
			payload.ChatUUID, info.ID, QueueDefault, err,
		)
	}
	// asynq keeps finished tasks readable for a while (Retention/archive), so a
	// read-back alone could still hand back a task no worker will ever run.
	if queued != nil && queued.State == asynq.TaskStateArchived {
		logging.Errorf(
			"bot-reply continuation (ci#1148): chat %s event %s landed on archived task %s; the interaction is left without a bot turn",
			payload.ChatUUID, payload.MessageUUID, info.ID,
		)
		return fmt.Errorf(
			"bot-reply continuation for chat %s resolved to archived task %s; no worker will run it",
			payload.ChatUUID, info.ID,
		)
	}
	return nil
}
