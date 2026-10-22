package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ci#744: the cron self-messaging tool surface (`scheduler_create`,
// `scheduler_list`, `scheduler_cancel`).
//
// The tools build on the one-shot scheduler of ci#724 (`coder_schedule`) and
// share its trust model: a schedule always belongs to the chat of the running
// turn, which the core injects into every tool of that turn. A model therefore
// can never schedule a message into someone else's chat, and `target` is only
// accepted as `self` (bot/interaction owner only).
//
// The tools themselves only validate and clamp the model's input. The
// persist-schedule-definition, single-active supersede and queue work live in
// the core, reached through the SchedulerRunner seam below — the same
// indirection CoderScheduleRunnerProvider uses.
const (
	// SchedulerCreateToolName is the cron self-schedule tool.
	SchedulerCreateToolName = "scheduler_create"
	// SchedulerListToolName lists this bot/interaction's schedules.
	SchedulerListToolName = "scheduler_list"
	// SchedulerCancelToolName cancels a schedule by key.
	SchedulerCancelToolName = "scheduler_cancel"
	// SchedulerDefaultKey is the schedule key a create without an explicit key
	// uses. It is the key the single-active lock supersedes.
	SchedulerDefaultKey = "self"
	// SchedulerMinDelaySeconds bounds `delay_seconds` (the first fire may be
	// delayed, never accelerated).
	SchedulerMinDelaySeconds = 0
	// SchedulerMaxDelaySeconds bounds `delay_seconds` to one day.
	SchedulerMaxDelaySeconds = 86400
	// SchedulerMaxJitterSeconds bounds the cron jitter.
	SchedulerMaxJitterSeconds = 300
	// SchedulerMaxRunsCap bounds `max_runs` so a loop cannot be pinned forever.
	SchedulerMaxRunsCap = 1000
)

// SchedulerCronExpr is a parsed 5-field (`min hour dom mon dow`) or 6-field
// (`sec min hour dom mon dow`) cron expression. It deliberately lives in the
// tools layer so the model's expression is rejected before it reaches the
// runtime; the runtime re-validates with its own parser.
type SchedulerCronExpr struct {
	raw      string
	seconds  []int
	minutes  []int
	hours    []int
	days     []int
	months   []int
	weekdays []int
	domStar  bool
	dowStar  bool
}

// ParseSchedulerCronExpr parses a 5/6-field cron expression.
func ParseSchedulerCronExpr(raw string) (*SchedulerCronExpr, error) {
	expression := strings.TrimSpace(raw)
	fields := strings.Fields(expression)
	if len(fields) != 5 && len(fields) != 6 {
		return nil, fmt.Errorf("cron %q must have 5 (min hour dom mon dow) or 6 (sec min hour dom mon dow) fields, got %d", raw, len(fields))
	}
	parsed := &SchedulerCronExpr{raw: expression, seconds: []int{0}}
	position := 0
	var err error
	if len(fields) == 6 {
		if parsed.seconds, _, err = parseSchedulerCronField(fields[0], 0, 59); err != nil {
			return nil, fmt.Errorf("cron %q has an invalid seconds field: %w", raw, err)
		}
		position = 1
	}
	if parsed.minutes, _, err = parseSchedulerCronField(fields[position], 0, 59); err != nil {
		return nil, fmt.Errorf("cron %q has an invalid minutes field: %w", raw, err)
	}
	if parsed.hours, _, err = parseSchedulerCronField(fields[position+1], 0, 23); err != nil {
		return nil, fmt.Errorf("cron %q has an invalid hours field: %w", raw, err)
	}
	if parsed.days, parsed.domStar, err = parseSchedulerCronField(fields[position+2], 1, 31); err != nil {
		return nil, fmt.Errorf("cron %q has an invalid day-of-month field: %w", raw, err)
	}
	if parsed.months, _, err = parseSchedulerCronField(fields[position+3], 1, 12); err != nil {
		return nil, fmt.Errorf("cron %q has an invalid month field: %w", raw, err)
	}
	weekdays, dowStar, err := parseSchedulerCronField(fields[position+4], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("cron %q has an invalid day-of-week field: %w", raw, err)
	}
	parsed.dowStar = dowStar
	// cron accepts both 0 and 7 for Sunday.
	parsed.weekdays = []int{}
	seen := map[int]bool{}
	for _, weekday := range weekdays {
		if weekday == 7 {
			weekday = 0
		}
		if seen[weekday] {
			continue
		}
		seen[weekday] = true
		parsed.weekdays = append(parsed.weekdays, weekday)
	}
	sort.Ints(parsed.weekdays)
	return parsed, nil
}

// parseSchedulerCronField expands one field into its sorted value set and
// reports whether the field was the unrestricted "*".
func parseSchedulerCronField(raw string, minimum int, maximum int) ([]int, bool, error) {
	field := strings.TrimSpace(raw)
	if field == "" {
		return nil, false, fmt.Errorf("empty cron field")
	}
	values := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, fmt.Errorf("empty element in %q", raw)
		}
		step := 1
		if index := strings.Index(part, "/"); index >= 0 {
			parsedStep, err := strconv.Atoi(strings.TrimSpace(part[index+1:]))
			if err != nil || parsedStep <= 0 {
				return nil, false, fmt.Errorf("invalid step in %q", part)
			}
			step = parsedStep
			part = strings.TrimSpace(part[:index])
		}
		low, high := minimum, maximum
		if part != "*" {
			if index := strings.Index(part, "-"); index >= 0 {
				parsedLow, err := strconv.Atoi(strings.TrimSpace(part[:index]))
				if err != nil {
					return nil, false, fmt.Errorf("invalid range in %q", part)
				}
				parsedHigh, err := strconv.Atoi(strings.TrimSpace(part[index+1:]))
				if err != nil {
					return nil, false, fmt.Errorf("invalid range in %q", part)
				}
				low, high = parsedLow, parsedHigh
			} else {
				parsed, err := strconv.Atoi(part)
				if err != nil {
					return nil, false, fmt.Errorf("invalid value %q", part)
				}
				low, high = parsed, parsed
			}
		}
		if low < minimum || high > maximum || low > high {
			return nil, false, fmt.Errorf("value %q is outside %d..%d", part, minimum, maximum)
		}
		for value := low; value <= high; value += step {
			values[value] = true
		}
	}
	if len(values) == 0 {
		return nil, false, fmt.Errorf("field %q matches nothing", raw)
	}
	expanded := make([]int, 0, len(values))
	for value := range values {
		expanded = append(expanded, value)
	}
	sort.Ints(expanded)
	return expanded, field == "*", nil
}

// Next returns the first instant strictly after `from` that matches the
// expression. `from` must already be in the schedule's location.
func (c *SchedulerCronExpr) Next(from time.Time) (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	limit := from.AddDate(1, 0, 0)
	for minute := from.Truncate(time.Minute); !minute.After(limit); minute = minute.Add(time.Minute) {
		if !c.matchesMinute(minute) {
			continue
		}
		for _, second := range c.seconds {
			candidate := minute.Add(time.Duration(second) * time.Second)
			if candidate.After(from) {
				return candidate, true
			}
		}
	}
	return time.Time{}, false
}

func (c *SchedulerCronExpr) matchesMinute(t time.Time) bool {
	if !containsSchedulerValue(c.minutes, t.Minute()) ||
		!containsSchedulerValue(c.hours, t.Hour()) ||
		!containsSchedulerValue(c.months, int(t.Month())) {
		return false
	}
	dayMatch := containsSchedulerValue(c.days, t.Day())
	weekdayMatch := containsSchedulerValue(c.weekdays, int(t.Weekday()))
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return weekdayMatch
	case c.dowStar:
		return dayMatch
	default:
		// Vixie cron: when both are restricted the union matches.
		return dayMatch || weekdayMatch
	}
}

func containsSchedulerValue(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// SchedulerSelfMessageRequest is the core-facing create request. It always
// targets the chat of the running turn ("self").
type SchedulerSelfMessageRequest struct {
	ChatUUID      string `json:"chat_uuid"`
	BotUUID       string `json:"bot_uuid,omitempty"`
	Key           string `json:"key"`
	Cron          string `json:"cron"`
	Timezone      string `json:"timezone,omitempty"`
	Prompt        string `json:"prompt"`
	Target        string `json:"target"`
	DelaySeconds  int    `json:"delay_seconds,omitempty"`
	MaxRuns       int    `json:"max_runs,omitempty"`
	JitterSeconds int    `json:"jitter_seconds,omitempty"`
	CorrelationID string `json:"id,omitempty"`
}

// SchedulerSelfMessageStatus is the observable state of one schedule: it is what
// `scheduler_list` returns and what status/logs/metrics expose.
type SchedulerSelfMessageStatus struct {
	Key            string `json:"key"`
	ChatUUID       string `json:"chat_uuid"`
	BotUUID        string `json:"bot_uuid,omitempty"`
	Cron           string `json:"cron,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
	State          string `json:"state"`
	NextRun        string `json:"next_run,omitempty"`
	LastRun        string `json:"last_run,omitempty"`
	RunsUsed       int    `json:"runs_used"`
	MaxRuns        int    `json:"max_runs,omitempty"`
	JitterSeconds  int    `json:"jitter_seconds,omitempty"`
	LastSkipReason string `json:"last_skip_reason,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

// SchedulerRunner is the core-side seam of the scheduler tools. It owns
// persistence, the single-active lock and the queue work. Create returns the
// winning status (superseded older schedules are cancelled, not returned).
type SchedulerRunner interface {
	CreateSchedulerSelfMessage(request SchedulerSelfMessageRequest) (SchedulerSelfMessageStatus, error)
	ListSchedulerSelfMessages(chatUUID string, botUUID string) ([]SchedulerSelfMessageStatus, error)
	CancelSchedulerSelfMessage(chatUUID string, botUUID string, key string, all bool) ([]string, error)
}

// SchedulerRunnerProvider returns the runner of the current deployment, or nil
// when no queue-owning process published one. A nil provider makes the tools
// fail loudly instead of silently dropping scheduled work.
var SchedulerRunnerProvider func() SchedulerRunner

// SchedulerCreateInput is the model-facing input of `scheduler_create`.
type SchedulerCreateInput struct {
	Cron          string `json:"cron,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	Target        string `json:"target,omitempty"`
	DelaySeconds  int    `json:"delay_seconds,omitempty"`
	MaxRuns       int    `json:"max_runs,omitempty"`
	JitterSeconds int    `json:"jitter_seconds,omitempty"`
	Key           string `json:"key,omitempty"`
	ID            string `json:"id,omitempty"`
}

// SchedulerListInput is the model-facing input of `scheduler_list`.
type SchedulerListInput struct {
	Key string `json:"key,omitempty"`
}

// SchedulerCancelInput is the model-facing input of `scheduler_cancel`.
type SchedulerCancelInput struct {
	Key string `json:"key,omitempty"`
	All bool   `json:"all,omitempty"`
}

type schedulerToolResult struct {
	Created       bool                         `json:"created,omitempty"`
	Cancelled     bool                         `json:"cancelled,omitempty"`
	Schedule      *SchedulerSelfMessageStatus  `json:"schedule,omitempty"`
	Schedules     []SchedulerSelfMessageStatus `json:"schedules,omitempty"`
	CancelledKeys []string                     `json:"cancelled_keys,omitempty"`
	ChatUUID      string                       `json:"chat_uuid"`
	Key           string                       `json:"key,omitempty"`
	ID            string                       `json:"id,omitempty"`
	Note          string                       `json:"note"`
}

var SchedulerCreateToolDef = ToolDefinition{
	Name:           SchedulerCreateToolName,
	Description:    "Create (or replace) this bot's cron self-message: a scheduled turn of this same chat. Only one schedule per bot/interaction is active at a time; creating a new one supersedes and cancels the older one. target defaults to \"self\" and no other target is accepted. cron is a 5-field (min hour dom mon dow) or 6-field (sec ...) expression, optionally evaluated in timezone (IANA name, default UTC). prompt is the continuation prompt posted on each fire; delay_seconds (0..86400) pushes the first fire out, max_runs caps the loop, jitter_seconds (0..300) spreads the fire. Scheduling is cheap and does not consume the current turn's tool budget.",
	InputType:      SchedulerCreateInput{},
	RequiresInit:   true,
	RequiredParams: []string{"cron", "prompt"},
	Parameters: map[string]interface{}{
		"cron":           map[string]interface{}{"type": "string", "description": "5/6-field cron expression, e.g. \"*/20 * * * *\"."},
		"timezone":       map[string]interface{}{"type": "string", "description": "Optional IANA timezone the expression is evaluated in (default UTC)."},
		"prompt":         map[string]interface{}{"type": "string", "description": "Continuation prompt posted on each fire."},
		"target":         map[string]interface{}{"type": "string", "description": "Only \"self\" (this chat) is accepted; defaults to self."},
		"delay_seconds":  map[string]interface{}{"type": "integer", "description": "Additional delay before the first fire, 0..86400."},
		"max_runs":       map[string]interface{}{"type": "integer", "description": "Maximum number of runs before the schedule stops itself, 1..1000."},
		"jitter_seconds": map[string]interface{}{"type": "integer", "description": "Random jitter added per fire, 0..300, to avoid fleet-wide spikes."},
		"key":            map[string]interface{}{"type": "string", "description": "Optional schedule key; defaults to \"self\"."},
		"id":             map[string]interface{}{"type": "string", "description": "Optional caller-side correlation id, echoed back."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		return runSchedulerCreate(input, init, currentSchedulerRunner())
	},
}

var SchedulerListToolDef = ToolDefinition{
	Name:           SchedulerListToolName,
	Description:    "List this bot/interaction's self-message schedules with next run, last run, runs used and state (including the reason of the last skip/defer and the last error). Scheduling is cheap and does not consume the current turn's tool budget.",
	InputType:      SchedulerListInput{},
	RequiresInit:   true,
	RequiredParams: []string{},
	Parameters: map[string]interface{}{
		"key": map[string]interface{}{"type": "string", "description": "Optional schedule key to report only one schedule."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		return runSchedulerList(input, init, currentSchedulerRunner())
	},
}

var SchedulerCancelToolDef = ToolDefinition{
	Name:           SchedulerCancelToolName,
	Description:    "Cancel this bot/interaction's cron self-message schedule so it stops firing. Use all=true to cancel every schedule of this bot; the bot then stays idle until an operator or a new schedule wakes it. Scheduling is cheap and does not consume the current turn's tool budget.",
	InputType:      SchedulerCancelInput{},
	RequiresInit:   true,
	RequiredParams: []string{},
	Parameters: map[string]interface{}{
		"key": map[string]interface{}{"type": "string", "description": "Schedule key to cancel; defaults to the single-active \"self\" schedule."},
		"all": map[string]interface{}{"type": "boolean", "description": "Cancel every schedule of this bot/interaction."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		return runSchedulerCancel(input, init, currentSchedulerRunner())
	},
}

func currentSchedulerRunner() SchedulerRunner {
	if SchedulerRunnerProvider == nil {
		return nil
	}
	return SchedulerRunnerProvider()
}

// schedulerIdentityFromInit reads the trusted chat/bot identity the core injects
// into a turn. A schedule never carries an identity of its own.
func schedulerIdentityFromInit(init map[string]interface{}) (chatUUID string, botUUID string) {
	chatUUID = strings.TrimSpace(chatUUIDFromInit(init))
	for _, key := range []string{"bot_uuid", "bot_id", "bot_user_uuid"} {
		if value, ok := init[key].(string); ok && strings.TrimSpace(value) != "" {
			botUUID = strings.TrimSpace(value)
			break
		}
	}
	return chatUUID, botUUID
}

func runSchedulerCreate(input interface{}, init map[string]interface{}, runner SchedulerRunner) (string, error) {
	in := castSchedulerInput[SchedulerCreateInput](input)
	chatUUID, botUUID := schedulerIdentityFromInit(init)
	if chatUUID == "" {
		return "", fmt.Errorf("%s needs the chat identity of the running turn", SchedulerCreateToolName)
	}
	cron := strings.TrimSpace(in.Cron)
	if cron == "" {
		return "", fmt.Errorf("%s requires a cron expression", SchedulerCreateToolName)
	}
	if _, err := ParseSchedulerCronExpr(cron); err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("%s requires the prompt of the scheduled self-message", SchedulerCreateToolName)
	}
	target := strings.ToLower(strings.TrimSpace(in.Target))
	if target != "" && target != "self" {
		return "", fmt.Errorf("%s can only schedule a message to this chat (target \"self\"), not %q", SchedulerCreateToolName, target)
	}
	timezone := strings.TrimSpace(in.Timezone)
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return "", fmt.Errorf("%s got an unknown timezone %q", SchedulerCreateToolName, timezone)
		}
	}
	maxRuns := in.MaxRuns
	if maxRuns < 0 {
		return "", fmt.Errorf("%s got a negative max_runs", SchedulerCreateToolName)
	}
	if maxRuns > SchedulerMaxRunsCap {
		maxRuns = SchedulerMaxRunsCap
	}
	jitter := in.JitterSeconds
	if jitter < 0 {
		jitter = 0
	}
	if jitter > SchedulerMaxJitterSeconds {
		jitter = SchedulerMaxJitterSeconds
	}
	delay := in.DelaySeconds
	if delay < SchedulerMinDelaySeconds {
		delay = SchedulerMinDelaySeconds
	}
	if delay > SchedulerMaxDelaySeconds {
		delay = SchedulerMaxDelaySeconds
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = SchedulerDefaultKey
	}
	if runner == nil {
		return "", fmt.Errorf("%s is unavailable in this deployment (no scheduler handles published)", SchedulerCreateToolName)
	}
	status, err := runner.CreateSchedulerSelfMessage(SchedulerSelfMessageRequest{
		ChatUUID:      chatUUID,
		BotUUID:       botUUID,
		Key:           key,
		Cron:          cron,
		Timezone:      timezone,
		Prompt:        prompt,
		Target:        "self",
		DelaySeconds:  delay,
		MaxRuns:       maxRuns,
		JitterSeconds: jitter,
		CorrelationID: strings.TrimSpace(in.ID),
	})
	if err != nil {
		return "", err
	}
	note := fmt.Sprintf("Self-message schedule %q is active for this bot; any previously active schedule was superseded. Cancel it with %s(key=%q).", status.Key, SchedulerCancelToolName, status.Key)
	return encodeSchedulerToolResult(schedulerToolResult{Created: true, Schedule: &status, ChatUUID: chatUUID, Key: status.Key, ID: strings.TrimSpace(in.ID), Note: note})
}

func runSchedulerList(input interface{}, init map[string]interface{}, runner SchedulerRunner) (string, error) {
	in := castSchedulerInput[SchedulerListInput](input)
	chatUUID, botUUID := schedulerIdentityFromInit(init)
	if chatUUID == "" {
		return "", fmt.Errorf("%s needs the chat identity of the running turn", SchedulerListToolName)
	}
	if runner == nil {
		return "", fmt.Errorf("%s is unavailable in this deployment (no scheduler handles published)", SchedulerListToolName)
	}
	schedules, err := runner.ListSchedulerSelfMessages(chatUUID, botUUID)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(in.Key)
	if key != "" {
		filtered := make([]SchedulerSelfMessageStatus, 0, 1)
		for _, schedule := range schedules {
			if schedule.Key == key {
				filtered = append(filtered, schedule)
			}
		}
		schedules = filtered
	}
	if schedules == nil {
		schedules = []SchedulerSelfMessageStatus{}
	}
	note := fmt.Sprintf("%d schedule(s) of this bot/interaction.", len(schedules))
	if len(schedules) == 0 {
		note = "No self-message schedule is active for this bot/interaction; it stays idle until a new schedule or an operator wakes it."
	}
	return encodeSchedulerToolResult(schedulerToolResult{Schedules: schedules, ChatUUID: chatUUID, Key: key, Note: note})
}

func runSchedulerCancel(input interface{}, init map[string]interface{}, runner SchedulerRunner) (string, error) {
	in := castSchedulerInput[SchedulerCancelInput](input)
	chatUUID, botUUID := schedulerIdentityFromInit(init)
	if chatUUID == "" {
		return "", fmt.Errorf("%s needs the chat identity of the running turn", SchedulerCancelToolName)
	}
	if runner == nil {
		return "", fmt.Errorf("%s is unavailable in this deployment (no scheduler handles published)", SchedulerCancelToolName)
	}
	key := strings.TrimSpace(in.Key)
	if key == "" && !in.All {
		key = SchedulerDefaultKey
	}
	keys, err := runner.CancelSchedulerSelfMessage(chatUUID, botUUID, key, in.All)
	if err != nil {
		return "", err
	}
	note := fmt.Sprintf("Cancelled %d self-message schedule(s); the bot no longer schedules itself.", len(keys))
	if len(keys) == 0 {
		note = "No matching self-message schedule was active."
	}
	return encodeSchedulerToolResult(schedulerToolResult{Cancelled: len(keys) > 0, CancelledKeys: keys, ChatUUID: chatUUID, Key: key, Note: note})
}

// castSchedulerInput accepts both value and pointer inputs, like the ci#724 tool.
func castSchedulerInput[T any](input interface{}) T {
	var zero T
	if value, ok := input.(T); ok {
		return value
	}
	if pointer, ok := input.(*T); ok && pointer != nil {
		return *pointer
	}
	return zero
}

func encodeSchedulerToolResult(result schedulerToolResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
