package msgmate

import (
	"fmt"
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
		if err := s.enqueue(slot.schedule, nextRun, slot.scheduleID); err != nil {
			return slot.schedule, true, err
		}
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
