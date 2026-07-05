package msgmate

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ci#744: the run policy that sits in front of the single-active scheduler.
//
// SelfMessageScheduler decides *when* a self-message fires (cron + one active
// slot per bot). This guard decides *whether* that fire may actually be
// delivered, and records why not - the missing half of "a scheduled run must be
// idempotent":
//
//   - a live turn defers the fire (recorded reason) instead of queueing a
//     duplicate, and the same cron slot is retried on the next tick,
//   - a cron slot that was already delivered is dropped, so a restart/replay
//     cannot double-run it,
//   - a schedule that used up its max_runs budget retires itself.
//
// Every decision is observable (next/last run, runs used, skips, skip reason,
// last error) through State/States, which is what scheduler_list and the status
// surface report.
const (
	// SelfMessageStateActive marks a schedule that may still fire.
	SelfMessageStateActive = "active"
	// SelfMessageStateRetired marks a schedule that reached its max_runs budget.
	SelfMessageStateRetired = "retired"
	// selfMessageSkipLiveTurn is the reason of a deferred fire.
	selfMessageSkipLiveTurn = "a turn is already live for this chat"
	// selfMessageSkipReplay marks a cron slot that was already delivered.
	selfMessageSkipReplay = "this cron slot was already delivered"
	// selfMessageSkipMaxRuns marks a schedule that spent its run budget.
	selfMessageSkipMaxRuns = "the schedule reached its max_runs budget"
)

// SelfMessageState is the observable state of one bot's self-message schedule.
type SelfMessageState struct {
	Key            string    `json:"key"`
	BotName        string    `json:"bot_name,omitempty"`
	ChatUUID       string    `json:"chat_uuid,omitempty"`
	Cron           string    `json:"cron"`
	ScheduleKey    string    `json:"schedule_key,omitempty"`
	State          string    `json:"state"`
	NextRun        time.Time `json:"next_run,omitempty"`
	LastRun        time.Time `json:"last_run,omitempty"`
	RunsUsed       int       `json:"runs_used"`
	MaxRuns        int       `json:"max_runs,omitempty"`
	Skips          int       `json:"skips"`
	LastSkipReason string    `json:"last_skip_reason,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
}

type selfMessageGuardSlot struct {
	state    SelfMessageState
	lastSlot time.Time
}

// SelfMessageGuard owns the run policy of every active schedule. It is safe to
// use without an idle check: a nil check never blocks a fire.
type SelfMessageGuard struct {
	mu    sync.Mutex
	slots map[string]*selfMessageGuardSlot
	idle  func(SelfMessageSchedule) (bool, string)
}

// NewSelfMessageGuard returns an empty run policy.
func NewSelfMessageGuard() *SelfMessageGuard {
	return &SelfMessageGuard{slots: map[string]*selfMessageGuardSlot{}}
}

// SetIdleCheck publishes the live-turn seam: it reports whether the chat of a
// schedule can accept the self-message right now, and the reason if it cannot.
func (g *SelfMessageGuard) SetIdleCheck(fn func(SelfMessageSchedule) (bool, string)) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.idle = fn
}

// Register activates a schedule (newest wins) and reports whether it superseded
// an earlier one - the caller cancels that older schedule on its side, so a bot
// never accumulates pending self-messages however often it schedules.
func (g *SelfMessageGuard) Register(schedule SelfMessageSchedule) (bool, error) {
	if g == nil {
		return false, nil
	}
	if schedule.MaxRuns < 0 {
		return false, fmt.Errorf("max_runs must not be negative, got %d", schedule.MaxRuns)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.slots == nil {
		g.slots = map[string]*selfMessageGuardSlot{}
	}
	key := schedule.Key()
	_, existed := g.slots[key]
	g.slots[key] = &selfMessageGuardSlot{state: SelfMessageState{
		Key:         key,
		BotName:     schedule.BotName,
		ChatUUID:    schedule.ChatUUID,
		Cron:        schedule.Cron,
		ScheduleKey: schedule.ScheduleKey,
		State:       SelfMessageStateActive,
		MaxRuns:     schedule.MaxRuns,
	}}
	return existed, nil
}

// Decide reports whether the self-message of a cron slot may be delivered. A
// deferred or retired decision is recorded and the schedule keeps its slot, so a
// deferral is retried on the next tick instead of queueing a duplicate.
func (g *SelfMessageGuard) Decide(schedule SelfMessageSchedule, slot time.Time) (bool, string) {
	if g == nil {
		return true, ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key := schedule.Key()
	entry, ok := g.slots[key]
	if !ok {
		return false, "no active schedule"
	}
	if entry.state.State == SelfMessageStateRetired {
		return false, selfMessageSkipMaxRuns
	}
	if slot.IsZero() {
		slot = time.Now()
	}
	slot = slot.UTC().Truncate(time.Second)
	entry.state.NextRun = slot
	if !entry.lastSlot.IsZero() && entry.lastSlot.Equal(slot) {
		return false, recordSelfMessageSkip(entry, selfMessageSkipReplay)
	}
	if entry.state.MaxRuns > 0 && entry.state.RunsUsed >= entry.state.MaxRuns {
		entry.state.State = SelfMessageStateRetired
		return false, recordSelfMessageSkip(entry, selfMessageSkipMaxRuns)
	}
	if g.idle != nil {
		if ready, reason := g.idle(schedule); !ready {
			if strings.TrimSpace(reason) == "" {
				reason = selfMessageSkipLiveTurn
			}
			return false, recordSelfMessageSkip(entry, reason)
		}
	}
	return true, ""
}

// RecordFire records a delivered cron slot and the run it consumed.
func (g *SelfMessageGuard) RecordFire(key string, slot time.Time) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.slots[key]
	if !ok {
		return
	}
	if slot.IsZero() {
		slot = time.Now()
	}
	entry.lastSlot = slot.UTC().Truncate(time.Second)
	entry.state.LastRun = entry.lastSlot
	entry.state.RunsUsed++
	entry.state.LastSkipReason = ""
}

// RecordError records the failure of a fire for the status/observability surface.
func (g *SelfMessageGuard) RecordError(key string, err error) {
	if g == nil || err == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if entry, ok := g.slots[key]; ok {
		entry.state.LastError = err.Error()
	}
}

// Retire stops future runs of a schedule and reports whether it was active.
func (g *SelfMessageGuard) Retire(key, reason string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.slots[key]
	if !ok {
		return false
	}
	entry.state.State = SelfMessageStateRetired
	if strings.TrimSpace(reason) != "" {
		entry.state.LastSkipReason = reason
	}
	return true
}

// Drop forgets a cancelled or superseded schedule.
func (g *SelfMessageGuard) Drop(key string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.slots, key)
}

// State returns the observable state of one schedule.
func (g *SelfMessageGuard) State(key string) (SelfMessageState, bool) {
	if g == nil {
		return SelfMessageState{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.slots[key]
	if !ok {
		return SelfMessageState{}, false
	}
	return entry.state, true
}

// States returns the observable state of every active schedule.
func (g *SelfMessageGuard) States() []SelfMessageState {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	states := make([]SelfMessageState, 0, len(g.slots))
	for _, entry := range g.slots {
		states = append(states, entry.state)
	}
	return states
}

// Len reports how many schedules the policy tracks.
func (g *SelfMessageGuard) Len() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.slots)
}

func recordSelfMessageSkip(entry *selfMessageGuardSlot, reason string) string {
	entry.state.Skips++
	entry.state.LastSkipReason = reason
	return reason
}

// SetIdleCheck publishes the live-turn seam of the run policy: the check reports
// whether the chat of a schedule is free to accept the self-message now.
func (r *SelfMessageRuntime) SetIdleCheck(fn func(SelfMessageSchedule) (bool, string)) {
	if r == nil {
		return
	}
	r.guard.SetIdleCheck(fn)
}

// States reports the observable state of every self-message schedule, i.e. the
// next/last run, runs used, skip reasons and the last error.
func (r *SelfMessageRuntime) States() []SelfMessageState {
	if r == nil {
		return nil
	}
	return r.guard.States()
}
