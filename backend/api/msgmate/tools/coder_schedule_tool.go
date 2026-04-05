package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// coder_schedule tool (ci#724): the trigger a lead agent uses to start a later
// turn on its own chat (dispatch -> monitor -> coder_schedule(600, "pulse")).
// The tool itself only validates and clamps the model's input; the actual
// persist-message-then-enqueue lives in the core, reached through the
// CoderScheduleRunner seam below.
const (
	CoderScheduleMinDelaySeconds = 60
	CoderScheduleMaxDelaySeconds = 3600
)

// CoderScheduleInput is the model-facing input. It carries no chat id on
// purpose: the scheduled turn always belongs to the trusted chat of the
// running turn, so a model can never schedule work in someone else's chat.
// Either a prompt (schedule) or a cancel_id (cancel) is set, never both.
type CoderScheduleInput struct {
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	ID           string `json:"id,omitempty"`
	CancelID     string `json:"cancel_id,omitempty"`
}

// CoderScheduleRunner is the core-side seam of the tool. The tools package
// cannot import the msgmate/chats packages it is embedded in, so the owning
// process publishes its runner through CoderScheduleRunnerProvider (the same
// indirection ContextUsageLookup uses).
type CoderScheduleRunner interface {
	// ScheduleCoderTurn persists prompt as a user-side message of chatUUID and
	// enqueues its bot reply delaySeconds later. It returns the queue task id
	// that cancels the scheduled turn.
	ScheduleCoderTurn(chatUUID string, prompt string, delaySeconds int, id string) (scheduleID string, err error)
	// CancelCoderTurn removes the scheduled turn with scheduleID and reports
	// whether a pending scheduled turn was found.
	CancelCoderTurn(chatUUID string, scheduleID string) (cancelled bool, err error)
}

// CoderScheduleRunnerProvider returns the runner of the current deployment, or
// nil when no queue-owning process published one. A nil provider makes the tool
// fail loudly instead of silently dropping scheduled work.
var CoderScheduleRunnerProvider func() CoderScheduleRunner

// ClampCoderScheduleDelay bounds a requested delay to the supported window, so
// a model cannot pin a turn for weeks (or fire one instantly) by accident.
func ClampCoderScheduleDelay(seconds int) int {
	if seconds < CoderScheduleMinDelaySeconds {
		return CoderScheduleMinDelaySeconds
	}
	if seconds > CoderScheduleMaxDelaySeconds {
		return CoderScheduleMaxDelaySeconds
	}
	return seconds
}

type coderScheduleResult struct {
	Scheduled    bool   `json:"scheduled,omitempty"`
	Cancelled    bool   `json:"cancelled,omitempty"`
	ScheduleID   string `json:"schedule_id,omitempty"`
	ChatUUID     string `json:"chat_uuid"`
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	ID           string `json:"id,omitempty"`
	Note         string `json:"note"`
}

var CoderScheduleToolDef = ToolDefinition{
	Name:        "coder_schedule",
	Description: "Schedule a later turn of this chat and return its schedule_id. Use it to keep a loop running without an external CronJob: coder_schedule(delay_seconds=600, prompt=\"pulse: ...\") queues the prompt as this chat's next turn, and coder_schedule(cancel_id=...) cancels a pending one. delay_seconds is clamped to 60..3600. Scheduling is cheap and does not consume the current turn's tool budget.",
	InputType:   CoderScheduleInput{},
	// RequiresInit: the schedule always targets the trusted chat of the running
	// turn, which the core injects into every tool of a chat turn.
	RequiresInit:   true,
	RequiredParams: []string{},
	Parameters: map[string]interface{}{
		"delay_seconds": map[string]interface{}{"type": "integer", "description": "Seconds until the scheduled turn starts; clamped to 60..3600 (default 60)."},
		"prompt":        map[string]interface{}{"type": "string", "description": "The prompt of the scheduled turn. Required unless cancel_id is set."},
		"id":            map[string]interface{}{"type": "string", "description": "Optional caller-side correlation id, echoed back in the result."},
		"cancel_id":     map[string]interface{}{"type": "string", "description": "schedule_id of a previously scheduled turn to cancel. Never combined with prompt."},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		var runner CoderScheduleRunner
		if CoderScheduleRunnerProvider != nil {
			runner = CoderScheduleRunnerProvider()
		}
		return runCoderSchedule(input, init, runner)
	},
}

// runCoderSchedule is the testable core of the tool: it validates the input,
// resolves the trusted chat and delegates to the runner.
func runCoderSchedule(input interface{}, init map[string]interface{}, runner CoderScheduleRunner) (string, error) {
	in, _ := input.(CoderScheduleInput)
	if pointer, ok := input.(*CoderScheduleInput); ok && pointer != nil {
		in = *pointer
	}
	chatUUID := strings.TrimSpace(chatUUIDFromInit(init))
	if chatUUID == "" {
		return "", fmt.Errorf("coder_schedule needs the chat identity of the running turn")
	}
	cancelID := strings.TrimSpace(in.CancelID)
	prompt := strings.TrimSpace(in.Prompt)
	correlationID := strings.TrimSpace(in.ID)
	if cancelID != "" && prompt != "" {
		return "", fmt.Errorf("coder_schedule takes either a prompt to schedule or a cancel_id to cancel, never both")
	}
	if runner == nil {
		return "", fmt.Errorf("coder_schedule is unavailable in this deployment (no queue handles published)")
	}
	if cancelID != "" {
		cancelled, err := runner.CancelCoderTurn(chatUUID, cancelID)
		if err != nil {
			return "", err
		}
		note := fmt.Sprintf("No pending scheduled turn %q was found in this chat.", cancelID)
		if cancelled {
			note = fmt.Sprintf("The scheduled turn %q is cancelled; it will not start.", cancelID)
		}
		return encodeCoderScheduleResult(coderScheduleResult{Cancelled: cancelled, ScheduleID: cancelID, ChatUUID: chatUUID, Note: note})
	}
	if prompt == "" {
		return "", fmt.Errorf("coder_schedule requires a prompt to schedule or a cancel_id to cancel")
	}
	delay := ClampCoderScheduleDelay(in.DelaySeconds)
	scheduleID, err := runner.ScheduleCoderTurn(chatUUID, prompt, delay, correlationID)
	if err != nil {
		return "", err
	}
	return encodeCoderScheduleResult(coderScheduleResult{
		Scheduled:    true,
		ScheduleID:   scheduleID,
		ChatUUID:     chatUUID,
		DelaySeconds: delay,
		Prompt:       prompt,
		ID:           correlationID,
		Note:         fmt.Sprintf("The next turn of this chat starts in %d seconds; cancel it with cancel_id=%q.", delay, scheduleID),
	})
}

func encodeCoderScheduleResult(result coderScheduleResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
