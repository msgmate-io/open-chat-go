package msgmate

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ci#744: cron-based self-messaging for bots/interactions.
//
// A bot can schedule a message to itself on a cron schedule, so a lead agent
// (the open-chat-go-ci development lead) can auto-resume its own loop without an
// external CronJob. The scheduler enforces a SINGLE-ACTIVE lock per bot:
// registering a schedule replaces (and cancels) the previous one of that bot,
// and a fire of a superseded schedule is dropped instead of being rescheduled.
// A bot therefore never accumulates pending self-messages, however often it
// schedules.
const (
	// SelfMessageDevelopmentLeadBotName is the bot the ci deployment configures
	// by default so it auto-resumes itself.
	SelfMessageDevelopmentLeadBotName = "msgmate-coder-development-lead"
	// SelfMessageDevelopmentLeadCron is that bot's default cadence.
	SelfMessageDevelopmentLeadCron = "*/20 * * * *"
	// SelfMessageScheduleEnvVar configures the per-bot schedules of a
	// deployment: a newline or semicolon separated list of "bot=<cron>" entries,
	// optionally "bot=<cron>@<timezone>". An entry overrides the built-in
	// default of that bot, an unknown bot adds a new schedule.
	SelfMessageScheduleEnvVar = "OPEN_CHAT_SELF_MESSAGE_SCHEDULES"
	// selfMessageTaskPrefix marks the queue task IDs owned by this scheduler.
	selfMessageTaskPrefix = "self-message:"
	// SelfMessageDefaultScheduleKey is the schedule key of a self-message that
	// did not name one: the default is to message the bot itself.
	SelfMessageDefaultScheduleKey = "self"
)

// SelfMessageSchedule is one registered cron self-message of a bot.
type SelfMessageSchedule struct {
	BotUserID uint   `json:"bot_user_id"`
	BotUUID   string `json:"bot_uuid,omitempty"`
	BotName   string `json:"bot_name,omitempty"`
	ChatUUID  string `json:"chat_uuid"`
	Prompt    string `json:"prompt"`
	Cron      string `json:"cron"`
	Timezone  string `json:"timezone,omitempty"`
	// ScheduleKey is the caller's stable label of the schedule (defaults to
	// "self"), so a bot can name and later cancel a schedule.
	ScheduleKey string `json:"schedule_key,omitempty"`
	// InitialDelay postpones the first fire; the cron cadence applies afterwards.
	InitialDelay time.Duration `json:"initial_delay,omitempty"`
	// Jitter spreads fires of the same cron instant by up to this much.
	Jitter time.Duration `json:"jitter,omitempty"`
	// MaxRuns retires the schedule after that many delivered runs (0 = unlimited).
	MaxRuns int `json:"max_runs,omitempty"`
	// OwnerKey is the ci#845 coordination owner key of the schedule's chat,
	// derived from the chat's own owner:/lane: tags. It is empty unless the
	// chat explicitly declares a lane, so un-laned chats keep their historical
	// behaviour.
	OwnerKey string `json:"owner_key,omitempty"`
}

// Key identifies the single-active slot a schedule belongs to: one slot per bot.
func (schedule SelfMessageSchedule) Key() string {
	if schedule.BotUserID != 0 {
		return fmt.Sprintf("user:%d", schedule.BotUserID)
	}
	return "uuid:" + strings.TrimSpace(schedule.BotUUID)
}

type selfMessageSlot struct {
	schedule   SelfMessageSchedule
	cron       *schedulerCron
	location   *time.Location
	scheduleID string
	nextRun    time.Time
	fires      int
	// Observability merged in from the run policy: runs delivered, deferrals and
	// their reason, the failure of the last fire and the schedule state.
	runs       int
	skips      int
	skipReason string
	lastError  string
	state      string
}

// SelfMessageScheduler owns the single active self-message schedule per bot.
// The queue-facing work is injected through SetEnqueue/SetCancel so the
// scheduler itself stays free of queue and database dependencies.
type SelfMessageScheduler struct {
	mu      sync.Mutex
	slots   map[string]*selfMessageSlot
	now     func() time.Time
	cancel  func(scheduleID string) error
	enqueue func(schedule SelfMessageSchedule, fireAt time.Time, scheduleID string) error
	nonce   func() string
}

func NewSelfMessageScheduler() *SelfMessageScheduler {
	return &SelfMessageScheduler{
		slots: map[string]*selfMessageSlot{},
		now:   time.Now,
		nonce: selfMessageNonce,
	}
}

// SetEnqueue publishes the queue-backed delivery of a due self-message.
func (s *SelfMessageScheduler) SetEnqueue(fn func(schedule SelfMessageSchedule, fireAt time.Time, scheduleID string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueue = fn
}

// SetCancel publishes the cancellation of a superseded self-message task.
func (s *SelfMessageScheduler) SetCancel(fn func(scheduleID string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = fn
}

// SetNow overrides the clock (tests).
func (s *SelfMessageScheduler) SetNow(fn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = fn
}

// Schedule registers (or replaces) the single active self-message schedule of a
// bot and returns the schedule id of the winning schedule. A previously active
// schedule of the same bot is cancelled and never rescheduled.
func (s *SelfMessageScheduler) Schedule(schedule SelfMessageSchedule) (string, error) {
	if s == nil {
		return "", fmt.Errorf("the self-message scheduler is not available")
	}
	if schedule.MaxRuns < 0 {
		return "", fmt.Errorf("the self-message schedule has a negative max_runs %d", schedule.MaxRuns)
	}
	if schedule.InitialDelay < 0 || schedule.Jitter < 0 {
		return "", fmt.Errorf("the self-message schedule has a negative initial delay or jitter")
	}
	schedule.ScheduleKey = strings.TrimSpace(schedule.ScheduleKey)
	if schedule.ScheduleKey == "" {
		schedule.ScheduleKey = SelfMessageDefaultScheduleKey
	}
	schedule.Prompt = strings.TrimSpace(schedule.Prompt)
	schedule.Cron = strings.TrimSpace(schedule.Cron)
	schedule.ChatUUID = strings.TrimSpace(schedule.ChatUUID)
	if schedule.BotUserID == 0 && strings.TrimSpace(schedule.BotUUID) == "" {
		return "", fmt.Errorf("a self-message schedule needs a bot")
	}
	if schedule.ChatUUID == "" {
		return "", fmt.Errorf("a self-message schedule needs the bot's chat")
	}
	if schedule.Prompt == "" {
		return "", fmt.Errorf("a self-message schedule needs a prompt")
	}
	cron, err := ParseSchedulerCron(schedule.Cron)
	if err != nil {
		return "", err
	}
	location := time.UTC
	if timezone := strings.TrimSpace(schedule.Timezone); timezone != "" {
		loaded, loadErr := time.LoadLocation(timezone)
		if loadErr != nil {
			return "", fmt.Errorf("self-message schedule has an unknown timezone %q: %w", timezone, loadErr)
		}
		location = loaded
	}

	// The lock is held across the cancel/enqueue seams on purpose: a bot must
	// never end up with two active slots because two schedules raced.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slots == nil {
		s.slots = map[string]*selfMessageSlot{}
	}
	key := schedule.Key()
	if previous, ok := s.slots[key]; ok {
		// SINGLE-ACTIVE: the older schedule is cancelled here and is never
		// rescheduled afterwards.
		delete(s.slots, key)
		if s.cancel != nil && previous.scheduleID != "" {
			if err := s.cancel(previous.scheduleID); err != nil {
				return "", fmt.Errorf("could not cancel the superseded self-message schedule: %w", err)
			}
		}
	}

	nextRun, ok := cron.Next(s.nowTime(), location)
	if !ok {
		return "", fmt.Errorf("the cron expression %q never fires", schedule.Cron)
	}
	// An initial delay postpones only the first fire; afterwards the cron
	// cadence applies unchanged, which is what an "auto-resume after a while"
	// schedule needs on (re)creation.
	if schedule.InitialDelay > 0 {
		nextRun = s.nowTime().Add(schedule.InitialDelay)
	}
	nextRun = s.applyJitter(schedule, nextRun)
	scheduleID := selfMessageTaskID(schedule, s.nextNonce())
	if s.enqueue != nil {
		if err := s.enqueue(schedule, nextRun, scheduleID); err != nil {
			return "", err
		}
	}
	s.slots[key] = &selfMessageSlot{
		schedule:   schedule,
		cron:       cron,
		location:   location,
		scheduleID: scheduleID,
		nextRun:    nextRun,
	}
	return scheduleID, nil
}

// Cancel removes the active self-message schedule of a bot and reports whether
// one was active.
func (s *SelfMessageScheduler) Cancel(key string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, ok := s.slots[key]
	if !ok {
		return false
	}
	delete(s.slots, key)
	if s.cancel != nil && slot.scheduleID != "" {
		if err := s.cancel(slot.scheduleID); err != nil {
			return false
		}
	}
	return true
}

// Active returns the single active schedule of a bot.
func (s *SelfMessageScheduler) Active(key string) (SelfMessageSchedule, bool) {
	if s == nil {
		return SelfMessageSchedule{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, ok := s.slots[key]
	if !ok {
		return SelfMessageSchedule{}, false
	}
	return slot.schedule, true
}

// ActiveTaskID returns the asynq task ID of a bot's active schedule. Fire
// rotates this ID on every fire (ci#744), so the runtime must re-read it after a
// successful fire instead of assuming the ID it registered with.
func (s *SelfMessageScheduler) ActiveTaskID(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, ok := s.slots[key]
	if !ok {
		return "", false
	}
	return slot.scheduleID, true
}

// NextRun returns the next fire instant of a bot's active schedule.
func (s *SelfMessageScheduler) NextRun(key string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, ok := s.slots[key]
	if !ok {
		return time.Time{}, false
	}
	return slot.nextRun, true
}

// Fire handles a due self-message of scheduleID. It reports false when the fire
// belongs to a schedule that a newer registration has already superseded: such a
// fire is dropped and never rescheduled (SINGLE-ACTIVE). The winning schedule is
// rescheduled for its next cron instant.
func (s *SelfMessageScheduler) Fire(key string, scheduleID string) (SelfMessageSchedule, bool, error) {
	if s == nil {
		return SelfMessageSchedule{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, ok := s.slots[key]
	if !ok || slot.scheduleID != scheduleID {
		return SelfMessageSchedule{}, false, nil
	}
	nextRun, ok := slot.cron.Next(s.nowTime(), slot.location)
	if !ok {
		delete(s.slots, key)
		return slot.schedule, false, fmt.Errorf("the cron expression %q never fires again", slot.schedule.Cron)
	}
	slot.nextRun = nextRun
	slot.fires++
	if s.enqueue != nil {
		// ci#744: a fresh task ID per fire. The delivery of the previous fire is
		// usually still active (a bot reply runs for minutes), so reusing the
		// prior ID makes asynq reject the next fire with ErrTaskIDConflict and
		// the schedule stalls forever after its first run.
		nextTaskID := selfMessageTaskID(slot.schedule, s.nextNonce())
		if err := s.enqueue(slot.schedule, nextRun, nextTaskID); err != nil {
			return slot.schedule, true, err
		}
		slot.scheduleID = nextTaskID
	}
	return slot.schedule, true, nil
}

// Len reports how many bots currently have an active self-message schedule.
func (s *SelfMessageScheduler) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.slots)
}

func (s *SelfMessageScheduler) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// applyJitter spreads the fires of a schedule by up to its Jitter budget, so a
// fleet of bots on the same cron instant does not stampede the queue.
func (s *SelfMessageScheduler) applyJitter(schedule SelfMessageSchedule, fireAt time.Time) time.Time {
	if schedule.Jitter <= 0 {
		return fireAt
	}
	offset := time.Duration(rand.Int63n(int64(schedule.Jitter)))
	return fireAt.Add(offset)
}

// Record is the observable state of one active schedule: what cron_list reports
// and what the status surface logs.
type Record struct {
	Key            string    `json:"key"`
	BotName        string    `json:"bot_name,omitempty"`
	ChatUUID       string    `json:"chat_uuid,omitempty"`
	ScheduleKey    string    `json:"schedule_key,omitempty"`
	Cron           string    `json:"cron"`
	Timezone       string    `json:"timezone,omitempty"`
	Prompt         string    `json:"prompt,omitempty"`
	MaxRuns        int       `json:"max_runs,omitempty"`
	InitialDelay   string    `json:"initial_delay,omitempty"`
	Jitter         string    `json:"jitter,omitempty"`
	NextRun        time.Time `json:"next_run,omitempty"`
	Fires          int       `json:"fires"`
	ScheduleID     string    `json:"schedule_id,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	State          string    `json:"state"`
	RunsUsed       int       `json:"runs_used"`
	Skips          int       `json:"skips"`
	LastSkipReason string    `json:"last_skip_reason,omitempty"`
}

// Records reports the active schedules of the scheduler, optionally restricted
// to one bot key.
func (s *SelfMessageScheduler) Records(key string) []Record {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]Record, 0, len(s.slots))
	for slotKey, slot := range s.slots {
		if key != "" && slotKey != key {
			continue
		}
		record := Record{
			Key:            slotKey,
			BotName:        slot.schedule.BotName,
			ChatUUID:       slot.schedule.ChatUUID,
			ScheduleKey:    slot.schedule.ScheduleKey,
			Cron:           slot.schedule.Cron,
			Timezone:       slot.schedule.Timezone,
			Prompt:         slot.schedule.Prompt,
			MaxRuns:        slot.schedule.MaxRuns,
			NextRun:        slot.nextRun,
			Fires:          slot.fires,
			ScheduleID:     slot.scheduleID,
			LastError:      slot.lastError,
			State:          SelfMessageStateActive,
			RunsUsed:       slot.runs,
			Skips:          slot.skips,
			LastSkipReason: slot.skipReason,
		}
		if slot.state != "" {
			record.State = slot.state
		}
		if slot.schedule.InitialDelay > 0 {
			record.InitialDelay = slot.schedule.InitialDelay.String()
		}
		if slot.schedule.Jitter > 0 {
			record.Jitter = slot.schedule.Jitter.String()
		}
		records = append(records, record)
	}
	return records
}

// RecordState merges the run-policy state (runs used, skips, skip reason) into a
// record, so one listing shows both the cron and the run history.
func (s *SelfMessageScheduler) RecordState(key string, state SelfMessageState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, ok := s.slots[key]
	if !ok {
		return
	}
	slot.runs = state.RunsUsed
	slot.skips = state.Skips
	slot.skipReason = state.LastSkipReason
	if state.LastError != "" {
		slot.lastError = state.LastError
	}
	if state.State != "" {
		slot.state = state.State
	}
}

// SetLastError records the failure of the last fire of a schedule.
func (s *SelfMessageScheduler) SetLastError(key string, err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slot, ok := s.slots[key]; ok {
		slot.lastError = err.Error()
	}
}

func (s *SelfMessageScheduler) nextNonce() string {
	if s.nonce != nil {
		return s.nonce()
	}
	return selfMessageNonce()
}

// SelfMessageScheduleEntry is one parsed per-bot schedule of a deployment.
type SelfMessageScheduleEntry struct {
	BotName  string
	Cron     string
	Timezone string
}

// ParseSelfMessageSchedules parses "bot=<cron>" entries separated by newlines or
// semicolons (commas are part of cron lists, so they cannot separate entries).
// Blank lines and "#" comments are ignored. An optional "@<timezone>" suffix
// selects the timezone the expression is evaluated in.
func ParseSelfMessageSchedules(spec string) ([]SelfMessageScheduleEntry, error) {
	entries := []SelfMessageScheduleEntry{}
	for _, rawEntry := range strings.FieldsFunc(spec, func(r rune) bool { return r == '\n' || r == ';' }) {
		entry := strings.TrimSpace(rawEntry)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		name, expression, found := strings.Cut(entry, "=")
		if !found {
			return nil, fmt.Errorf("self-message schedule %q is not a bot=<cron> entry", entry)
		}
		parsed := SelfMessageScheduleEntry{BotName: strings.TrimSpace(name)}
		if parsed.BotName == "" {
			return nil, fmt.Errorf("self-message schedule %q has no bot name", entry)
		}
		expression = strings.TrimSpace(expression)
		if index := strings.LastIndex(expression, "@"); index >= 0 {
			parsed.Timezone = strings.TrimSpace(expression[index+1:])
			expression = strings.TrimSpace(expression[:index])
		}
		if parsed.Timezone != "" {
			if _, err := time.LoadLocation(parsed.Timezone); err != nil {
				return nil, fmt.Errorf("self-message schedule of %s has an unknown timezone %q: %w", parsed.BotName, parsed.Timezone, err)
			}
		}
		if _, err := ParseSchedulerCron(expression); err != nil {
			return nil, fmt.Errorf("self-message schedule of %s is invalid: %w", parsed.BotName, err)
		}
		parsed.Cron = expression
		entries = append(entries, parsed)
	}
	return entries, nil
}

// SelfMessageSchedulesFromEnv resolves the per-bot schedules of a deployment:
// the built-in development-lead default, overridden or extended by
// SelfMessageScheduleEnvVar.
func SelfMessageSchedulesFromEnv(getenv func(string) string) ([]SelfMessageScheduleEntry, error) {
	entries := []SelfMessageScheduleEntry{{
		BotName: SelfMessageDevelopmentLeadBotName,
		Cron:    SelfMessageDevelopmentLeadCron,
	}}
	if getenv == nil {
		getenv = os.Getenv
	}
	spec := strings.TrimSpace(getenv(SelfMessageScheduleEnvVar))
	if spec == "" {
		return entries, nil
	}
	overrides, err := ParseSelfMessageSchedules(spec)
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for position, entry := range entries {
		index[strings.ToLower(entry.BotName)] = position
	}
	for _, override := range overrides {
		lower := strings.ToLower(override.BotName)
		if position, ok := index[lower]; ok {
			entries[position] = override
			continue
		}
		index[lower] = len(entries)
		entries = append(entries, override)
	}
	return entries, nil
}

// SelfMessageScheduleForBot looks up the configured schedule of one bot.
func SelfMessageScheduleForBot(entries []SelfMessageScheduleEntry, botName string) (SelfMessageScheduleEntry, bool) {
	botName = strings.ToLower(strings.TrimSpace(botName))
	for _, entry := range entries {
		if strings.ToLower(entry.BotName) == botName {
			return entry, true
		}
	}
	return SelfMessageScheduleEntry{}, false
}

var selfMessageNonceCounter int64

func selfMessageNonce() string {
	return fmt.Sprintf("%x%x", time.Now().UnixNano(), atomic.AddInt64(&selfMessageNonceCounter, 1))
}

func selfMessageTaskID(schedule SelfMessageSchedule, nonce string) string {
	bot := strings.TrimSpace(schedule.BotUUID)
	if bot == "" {
		bot = fmt.Sprintf("user-%d", schedule.BotUserID)
	}
	return selfMessageTaskPrefix + bot + ":" + nonce
}
