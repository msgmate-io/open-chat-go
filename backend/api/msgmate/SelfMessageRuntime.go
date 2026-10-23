package msgmate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"backend/database"
	"backend/logging"
	"backend/workqueue"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// ci#744: the runtime side of the cron self-messaging scheduler.
//
// SelfMessageScheduler owns the single-active bookkeeping but is deliberately
// free of queue/database dependencies: this file publishes the two seams it
// needs (enqueue/cancel), resolves the configured bot names to their newest
// chat, and drives the schedule forward on a ticker.
//
// Delivery is an ordinary bot:reply task enqueued with asynq.ProcessIn(next
// cron instant), exactly like coder_schedule (ci#724), so the loop stays inside
// Open-Chat with no external CronJob. The ticker only advances a slot whose
// instant has passed (Fire reschedules the next instant and enqueues exactly one
// delivery for it), so a slot is delivered once per cron instant.
const (
	// SelfMessageTickEnvVar tunes the runtime's check interval.
	SelfMessageTickEnvVar = "OPEN_CHAT_SELF_MESSAGE_TICK"
	// selfMessageMetadataKey marks a persisted self-message so the transcript
	// and the model can tell it apart from a typed message.
	selfMessageMetadataKey = "self_message_schedule"
	// SelfMessageContinuationPrompt is the prompt a scheduled self-message
	// carries: the bot resumes its own loop.
	SelfMessageContinuationPrompt = "Scheduled self-resume: continue the work you were doing. If it is complete, cancel your schedule and report status."
	// selfMessageDefaultTick is the ticker interval unless the env var overrides it.
	selfMessageDefaultTick = 30 * time.Second
)

// SelfMessageRuntime resolves the configured self-message bots and drives their
// schedules. It is safe to construct without a queue (tests): the enqueue/cancel
// seams then only persist/log.
type SelfMessageRuntime struct {
	scheduler *SelfMessageScheduler
	db        *gorm.DB
	client    *asynq.Client
	inspector *asynq.Inspector
	entries   []SelfMessageScheduleEntry
	tick      time.Duration
	now       func() time.Time
	guard     *SelfMessageGuard

	mu        sync.Mutex
	ids       map[string]string
	chats     map[string]string
	schedules map[string]SelfMessageSchedule

	stop chan struct{}
	once sync.Once
}

// StartSelfMessageRuntime resolves SelfMessageSchedulesFromEnv and starts the
// runtime. It returns (nil, nil) when no schedule is configured, so the caller
// can wire it unconditionally.
func StartSelfMessageRuntime(ctx context.Context, db *gorm.DB, client *asynq.Client, inspector *asynq.Inspector) (*SelfMessageRuntime, error) {
	entries, err := SelfMessageSchedulesFromEnv(os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", SelfMessageScheduleEnvVar, err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	interval := selfMessageDefaultTick
	if raw := strings.TrimSpace(os.Getenv(SelfMessageTickEnvVar)); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid %s %q: %w", SelfMessageTickEnvVar, raw, parseErr)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("%s must be positive, got %q", SelfMessageTickEnvVar, raw)
		}
		interval = parsed
	}
	runtime := newSelfMessageRuntime(db, client, inspector, NewSelfMessageScheduler(), entries, interval, time.Now)
	runtime.publishSeams()
	if err := runtime.RegisterConfiguredBots(); err != nil {
		return nil, err
	}
	runtime.Start(ctx)
	return runtime, nil
}

// newSelfMessageRuntime is the injectable constructor used by tests.
func newSelfMessageRuntime(db *gorm.DB, client *asynq.Client, inspector *asynq.Inspector, scheduler *SelfMessageScheduler, entries []SelfMessageScheduleEntry, tick time.Duration, now func() time.Time) *SelfMessageRuntime {
	if scheduler == nil {
		scheduler = NewSelfMessageScheduler()
	}
	if now == nil {
		now = time.Now
	}
	if tick <= 0 {
		tick = selfMessageDefaultTick
	}
	return &SelfMessageRuntime{
		scheduler: scheduler,
		db:        db,
		client:    client,
		inspector: inspector,
		entries:   entries,
		tick:      tick,
		now:       now,
		guard:     NewSelfMessageGuard(),
		ids:       map[string]string{},
		chats:     map[string]string{},
		schedules: map[string]SelfMessageSchedule{},
		stop:      make(chan struct{}),
	}
}

// publishSeams wires the scheduler's queue seams to this runtime.
func (r *SelfMessageRuntime) publishSeams() {
	r.scheduler.SetEnqueue(r.enqueue)
	r.scheduler.SetCancel(r.cancel)
	r.scheduler.SetNow(r.now)
	// ci#744: the default run policy defers a self-message while its chat
	// already has a live or queued turn (skip + recorded reason instead of a
	// duplicate reply). A deployment can tighten this via SetIdleCheck, e.g.
	// with the provider/sandbox admission check.
	r.guard.SetIdleCheck(r.defaultIdleCheck)
}

// defaultIdleCheck defers a self-message when a bot:reply turn is already live
// (or queued, pending or retrying) for the same chat, so a scheduled run can
// never duplicate a turn that is already in flight.
func (r *SelfMessageRuntime) defaultIdleCheck(schedule SelfMessageSchedule) (bool, string) {
	if r.inspector == nil || schedule.ChatUUID == "" {
		return true, ""
	}
	if workqueue.BotReplyHasWork(r.inspector, schedule.ChatUUID) {
		return false, selfMessageSkipLiveTurn
	}
	return true, ""
}

// Start runs the reconcile/advance ticker until ctx is cancelled or Shutdown is
// called.
func (r *SelfMessageRuntime) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(r.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.stop:
				return
			case <-ticker.C:
				if err := r.Tick(); err != nil {
					logging.Errorf("ci#744: self-message tick failed: %v", err)
				}
			}
		}
	}()
}

// Shutdown stops the ticker.
func (r *SelfMessageRuntime) Shutdown() {
	if r == nil {
		return
	}
	r.once.Do(func() { close(r.stop) })
}

// RegisterConfiguredBots reconciles every configured bot with its newest chat:
// a bot whose newest chat changed is re-registered, which cancels (single
// active) the older schedule instead of duplicating it. Failures are logged and
// skipped so one misconfigured bot cannot stop the others.
func (r *SelfMessageRuntime) RegisterConfiguredBots() error {
	var firstErr error
	for _, entry := range r.entries {
		if err := r.registerEntry(entry); err != nil {
			logging.Errorf("ci#744: self-message schedule of bot %q is inactive: %v", entry.BotName, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (r *SelfMessageRuntime) registerEntry(entry SelfMessageScheduleEntry) error {
	if r.db == nil {
		return fmt.Errorf("self-messaging is unavailable: no database")
	}
	bot, chat, err := selfMessageResolveBotChat(r.db, entry.BotName)
	if err != nil {
		return err
	}
	schedule := SelfMessageSchedule{
		BotUserID: bot.ID,
		BotUUID:   bot.UUID,
		BotName:   selfMessageBotLabel(bot),
		ChatUUID:  chat.UUID,
		Prompt:    SelfMessageContinuationPrompt,
		Cron:      entry.Cron,
		Timezone:  entry.Timezone,
	}
	// ci#845: the schedule inherits the ticket+lane of its chat when the chat
	// carries the dispatcher's `issue:`/`lane:` tags. Nothing is derived from
	// the chat id or the schedule key: an untagged chat stays un-keyed.
	if ticket, lane, keyed := CoordinationTicketAndLane("", "", []string(chat.Tags)); keyed {
		schedule.Ticket = ticket
		schedule.Lane = lane
	}
	key := schedule.Key()

	r.mu.Lock()
	_, registered := r.ids[key]
	previousChat := r.chats[key]
	previousSchedule := r.schedules[key]
	r.mu.Unlock()
	// Idempotent reconcile: re-registering unchanged every tick would cancel and
	// re-enqueue the same schedule on every tick.
	if registered && previousChat == chat.UUID {
		return nil
	}

	scheduleID, err := r.scheduler.Schedule(schedule)
	if err != nil {
		return err
	}
	entrySuperseded, err := r.guard.Register(schedule)
	if err != nil {
		// A rejected policy (e.g. a negative max_runs) must not leave a queue
		// task behind: retract the schedule we just armed.
		r.scheduler.Cancel(key)
		return err
	}
	if entrySuperseded {
		// A newer schedule supersedes the older one; the scheduler already
		// retracted its pending delivery, so only the state is dropped here.
		logging.Infof("ci#744: self-message schedule of %s replaced by a newer one (single active)", schedule.BotName)
	}
	r.mu.Lock()
	r.ids[key] = scheduleID
	r.chats[key] = chat.UUID
	r.schedules[key] = schedule
	r.mu.Unlock()
	if registered {
		// ci#845: a schedule that is replaced by one naming a different
		// ticket+lane must not keep the old lane busy (eg the chat was retagged
		// or rotated onto a new unit of work).
		oldKey := CoordinationDedupeKey(previousSchedule.Ticket, previousSchedule.Lane)
		newKey := CoordinationDedupeKey(schedule.Ticket, schedule.Lane)
		if oldKey != "" && oldKey != newKey {
			DefaultCoordinationOwnerGuard().Release(previousSchedule.Ticket, previousSchedule.Lane)
		}
	}
	logging.Infof("ci#744: self-message schedule of %s (%s, chat %s) active: cron %q", schedule.BotName, key, chat.UUID, schedule.Cron)
	return nil
}

// Tick reconciles the configured bots and advances every slot whose instant has
// passed. A fire of a superseded schedule reports won=false and is forgotten.
//
// Before a due slot is delivered the run policy decides whether it may run:
// a live turn or an already-delivered cron slot defers it (recorded reason,
// retried on the next tick) instead of queueing a duplicate.
func (r *SelfMessageRuntime) Tick() error {
	reconcileErr := r.RegisterConfiguredBots()

	r.mu.Lock()
	active := make(map[string]string, len(r.ids))
	for key, id := range r.ids {
		active[key] = id
	}
	r.mu.Unlock()

	now := r.now()
	coordinationGuard := DefaultCoordinationOwnerGuard()
	for key, scheduleID := range active {
		nextRun, ok := r.scheduler.NextRun(key)
		if !ok || nextRun.After(now) {
			continue
		}
		schedule, ok := r.scheduler.Active(key)
		if !ok {
			continue
		}
		if allowed, reason := r.guard.Decide(schedule, nextRun); !allowed {
			logging.Infof("ci#744: self-message of %s deferred: %s", key, reason)
			continue
		}
		// ci#845: a scheduled turn is a dispatch of its ticket+lane too, so it
		// shares the owner key of the other dispatcher paths. It is owner-keyed
		// only when it names a ticket or a lane (through the chat's
		// `issue:`/`lane:` tags): an unnamed schedule must not invent an owner,
		// and it never starts a second interaction for a live one.
		ownerKeyed := schedule.Ticket != "" || schedule.Lane != ""
		if ownerKeyed {
			coordination := coordinationGuard.Decide(CoordinationDispatchRequest{
				Ticket: schedule.Ticket,
				Lane:   schedule.Lane,
				Source: CoordinationSourceSelfMessageSchedule,
			})
			if coordination.Refused() {
				logging.Infof("ci#845: self-message of %s deferred (ticket %q, lane %q): %s", key, schedule.Ticket, schedule.Lane, coordination.Reason)
				continue
			}
			if !coordination.StartsNewInteraction() {
				logging.Infof("ci#845: self-message of %s re-attaches to the live owner %q of %s instead of a second interaction (%s): %s", key, coordination.InteractionUUID, coordination.OwnerKey, coordination.Source, coordination.Reason)
				continue
			}
		}
		_, won, err := r.scheduler.Fire(key, scheduleID)
		if err != nil {
			r.guard.RecordError(key, err)
			if ownerKeyed {
				coordinationGuard.Release(schedule.Ticket, schedule.Lane)
			}
			logging.Errorf("ci#744: self-message fire of %s failed: %v", key, err)
			continue
		}
		if !won {
			r.forget(key)
			continue
		}
		r.guard.RecordFire(key, nextRun)
		// The owner key stays held by this schedule: Decide above either
		// registered it for the first time or recognised this schedule as the
		// existing owner, so the next run of the same ticket+lane is accepted as
		// the same owner while a workflow dispatch of the same ticket+lane
		// re-attaches instead of starting a duplicate interaction. The owner is
		// given back only when the schedule is forgotten.
	}
	return reconcileErr
}

func (r *SelfMessageRuntime) forget(key string) {
	r.mu.Lock()
	schedule, hasSchedule := r.schedules[key]
	delete(r.ids, key)
	delete(r.chats, key)
	delete(r.schedules, key)
	r.mu.Unlock()
	if hasSchedule {
		// ci#845: a forgotten schedule no longer owns its ticket+lane, so the
		// lane is free for the dispatcher again.
		DefaultCoordinationOwnerGuard().Release(schedule.Ticket, schedule.Lane)
	}
	r.guard.Drop(key)
}

// enqueue persists the self-message of schedule.ChatUUID and enqueues its
// bot:reply with ProcessIn(fireAt). The scheduler's schedule ID is used as the
// asynq task ID so Cancel can retract exactly this delivery.
func (r *SelfMessageRuntime) enqueue(schedule SelfMessageSchedule, fireAt time.Time, scheduleID string) error {
	if r.db == nil {
		return fmt.Errorf("self-messaging is unavailable: no database")
	}
	var chat database.Chat
	if err := r.db.Preload("User1").Preload("User2").Where("uuid = ?", schedule.ChatUUID).First(&chat).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("self-message chat %s not found", schedule.ChatUUID)
		}
		return err
	}
	bot, user := coderScheduleParticipants(chat)
	if bot == nil {
		return fmt.Errorf("chat %s has no automated participant for a self-message", schedule.ChatUUID)
	}
	// Target defaults to self: when the chat has no user side the bot addresses
	// itself, which is the self-messaging case this scheduler exists for.
	sender := bot
	if user != nil {
		sender = user
	}
	if schedule.BotUserID != 0 && bot.ID != schedule.BotUserID {
		return fmt.Errorf("chat %s no longer belongs to bot %s", schedule.ChatUUID, schedule.BotName)
	}

	metadata, err := json.Marshal(map[string]interface{}{
		selfMessageMetadataKey:      true,
		"self_message_cron":         schedule.Cron,
		"self_message_task_id":      scheduleID,
		"self_message_scheduled_at": fireAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}

	text := schedule.Prompt
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   sender.ID,
		ReceiverId: bot.ID,
		Text:       &text,
		DataType:   "text",
		MetaData:   database.JSONRaw(metadata),
	}
	if err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		return tx.Model(&database.Chat{}).Where("id = ?", chat.ID).Update("latest_message_id", message.ID).Error
	}); err != nil {
		return err
	}

	if r.client == nil {
		// No queue published: the message is persisted and the ticker keeps
		// advancing the schedule, so a deployment without a queue degrades to a
		// no-op instead of failing the fire.
		return nil
	}
	task, err := workqueue.NewBotReplyTask(workqueue.BotReplyPayload{
		ChatUUID:    chat.UUID,
		MessageUUID: message.UUID,
		BotUserID:   bot.ID,
	})
	if err != nil {
		return err
	}
	delay := time.Until(fireAt)
	if delay < 0 {
		delay = 0
	}
	info, err := r.client.Enqueue(task,
		asynq.Queue(workqueue.QueueDefault),
		asynq.TaskID(scheduleID),
		asynq.ProcessIn(delay),
		asynq.MaxRetry(10),
		asynq.Timeout(workqueue.BotReplyTimeout()),
		asynq.Retention(0),
	)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("the self-message of %s enqueued no task", schedule.BotName)
	}
	return nil
}

// cancel retracts a superseded self-message delivery. Ownership is proven by the
// task-ID prefix, so the scheduler can never delete an unrelated queue task.
func (r *SelfMessageRuntime) cancel(scheduleID string) error {
	if r.inspector == nil || !strings.HasPrefix(scheduleID, selfMessageTaskPrefix) {
		return nil
	}
	if _, err := r.inspector.GetTaskInfo(workqueue.QueueDefault, scheduleID); err != nil {
		// Already delivered/cancelled: nothing to retract.
		return nil
	}
	if err := r.inspector.DeleteTask(workqueue.QueueDefault, scheduleID); err != nil {
		return err
	}
	return nil
}

// selfMessageResolveBotChat resolves a configured bot name to the bot user and
// its newest chat ("the most recent interaction").
func selfMessageResolveBotChat(db *gorm.DB, botName string) (database.User, database.Chat, error) {
	var bot database.User
	err := db.Where("username = ?", botName).Or("name = ?", botName).First(&bot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return bot, database.Chat{}, fmt.Errorf("bot %q is not a user of this deployment", botName)
		}
		return bot, database.Chat{}, err
	}
	if !bot.IsAutomated {
		return bot, database.Chat{}, fmt.Errorf("user %q is not an automated bot", botName)
	}
	var chat database.Chat
	err = db.Preload("User1").Preload("User2").
		Where("user1_id = ? OR user2_id = ?", bot.ID, bot.ID).
		Order("latest_message_id DESC").Order("id DESC").
		First(&chat).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return bot, chat, fmt.Errorf("bot %q has no chat to resume", botName)
		}
		return bot, chat, err
	}
	return bot, chat, nil
}

func selfMessageBotLabel(bot database.User) string {
	if name := strings.TrimSpace(bot.Username); name != "" {
		return name
	}
	return strings.TrimSpace(bot.Name)
}
