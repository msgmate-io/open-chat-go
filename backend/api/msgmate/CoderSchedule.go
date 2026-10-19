package msgmate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/database"
	"backend/workqueue"

	tooldefs "backend/api/msgmate/tools"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// ci#724: the core side of the `coder_schedule` tool.
//
// The tool lives in backend/api/msgmate/tools, which cannot import the packages
// it is embedded in, so it reaches the core through
// tooldefs.CoderScheduleRunnerProvider; coderScheduleRunner below is the
// implementation this process publishes from ToolConstructors' init().
//
// A scheduled turn is an ordinary user-side message of the caller's own chat
// whose bot reply is enqueued with asynq.ProcessIn(delay): the loop stays
// inside Open-Chat, with no external CronJob and no second scheduler.
const (
	// coderScheduleTaskPrefix marks the task IDs owned by this file. Cancelling
	// only ever deletes an ID with this prefix that also carries the calling
	// chat's UUID, so the tool can never remove an unrelated queue task.
	coderScheduleTaskPrefix = "coder-schedule:"
	// coderScheduleMetadataKey marks the persisted message of a scheduled turn
	// so a transcript (and the model) can tell it apart from a typed message.
	coderScheduleMetadataKey = "coder_schedule"
)

// coderScheduleRunner implements tooldefs.CoderScheduleRunner for this process.
type coderScheduleRunner struct{}

// The seams are package-level variables (the autoContinueEnqueue pattern) so a
// test can drive the runner against a scratch database and an in-process queue
// instead of the process-wide handles published by cmd/server.go.
var (
	coderScheduleGlobalDB    = database.GetGlobalDB
	coderScheduleQueueClient = func() *asynq.Client {
		client, _ := workqueue.GetGlobalQueueHandles()
		return client
	}
	coderScheduleInspector = func() *asynq.Inspector {
		_, inspector := workqueue.GetGlobalQueueHandles()
		return inspector
	}
	coderScheduleNow = time.Now
)

// ScheduleCoderTurn persists prompt as a user-side message of chatUUID and
// enqueues the bot reply delaySeconds later. It returns the queue task ID that
// cancels the scheduled turn.
func (coderScheduleRunner) ScheduleCoderTurn(chatUUID string, prompt string, delaySeconds int, correlationID string) (string, error) {
	chatUUID = strings.TrimSpace(chatUUID)
	prompt = strings.TrimSpace(prompt)
	if chatUUID == "" {
		return "", fmt.Errorf("coder_schedule needs the chat identity of the running turn")
	}
	if prompt == "" {
		return "", fmt.Errorf("coder_schedule needs a prompt to schedule")
	}
	if delaySeconds <= 0 {
		return "", fmt.Errorf("coder_schedule needs a positive delay")
	}

	db := coderScheduleGlobalDB()
	if db == nil {
		return "", fmt.Errorf("coder_schedule is unavailable: this process published no database")
	}
	client := coderScheduleQueueClient()
	if client == nil {
		return "", fmt.Errorf("coder_schedule is unavailable: this process published no queue handles")
	}

	var chat database.Chat
	if err := db.Preload("User1").Preload("User2").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("chat %s not found", chatUUID)
		}
		return "", err
	}
	bot, user := coderScheduleParticipants(chat)
	if bot == nil {
		return "", fmt.Errorf("chat %s has no automated participant, so a scheduled turn would go unanswered", chatUUID)
	}
	if user == nil {
		return "", fmt.Errorf("chat %s has no user-side participant to carry the scheduled prompt", chatUUID)
	}

	metadata, err := json.Marshal(map[string]interface{}{
		coderScheduleMetadataKey:                  true,
		"coder_schedule_delay_seconds":            delaySeconds,
		"coder_schedule_scheduled_by_correlation": correlationID,
	})
	if err != nil {
		return "", err
	}

	text := prompt
	scheduled := database.Message{
		ChatId:     chat.ID,
		SenderId:   user.ID,
		ReceiverId: bot.ID,
		Text:       &text,
		DataType:   "text",
		MetaData:   database.JSONRaw(metadata),
	}
	// Persist the message and advance the chat's latest_message_id in one
	// transaction (the scheduleAutoContinuePrompt shape), so a reader never sees
	// a chat pointing at a message that does not exist.
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&scheduled).Error; err != nil {
			return err
		}
		return tx.Model(&database.Chat{}).Where("id = ?", chat.ID).Update("latest_message_id", scheduled.ID).Error
	}); err != nil {
		return "", err
	}

	task, err := workqueue.NewBotReplyTask(workqueue.BotReplyPayload{
		ChatUUID:    chat.UUID,
		MessageUUID: scheduled.UUID,
		BotUserID:   bot.ID,
	})
	if err != nil {
		return "", err
	}
	scheduleID := coderScheduleTaskID(chat.UUID, correlationID)
	// Enqueue directly instead of through workqueue.EnqueueBotReply: the idle
	// path of that helper calls CancelBotReplyTask, which clears *every* reply
	// task of the chat and would therefore drop the other scheduled turns.
	info, err := client.Enqueue(task,
		asynq.Queue(workqueue.QueueDefault),
		asynq.TaskID(scheduleID),
		asynq.ProcessIn(time.Duration(delaySeconds)*time.Second),
		asynq.MaxRetry(10),
		asynq.Timeout(workqueue.BotReplyTimeout()),
		asynq.Retention(0),
	)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", fmt.Errorf("coder_schedule enqueued no task")
	}
	return info.ID, nil
}

// CancelCoderTurn removes the scheduled turn scheduleID and reports whether a
// pending scheduled turn was found.
//
// It deletes exactly that task ID on purpose: the chat-wide
// workqueue.CancelBotReplyTask would also clear the reply of a live or a
// user-initiated turn, which is not what "cancel my scheduled turn" means.
func (coderScheduleRunner) CancelCoderTurn(chatUUID string, scheduleID string) (bool, error) {
	chatUUID = strings.TrimSpace(chatUUID)
	scheduleID = strings.TrimSpace(scheduleID)
	if chatUUID == "" {
		return false, fmt.Errorf("coder_schedule needs the chat identity of the running turn")
	}
	if scheduleID == "" {
		return false, fmt.Errorf("coder_schedule cancel needs a schedule_id")
	}
	// Ownership first: only ever touch a task this tool created for this chat.
	if !strings.HasPrefix(scheduleID, coderScheduleTaskPrefix+chatUUID+":") {
		return false, fmt.Errorf("%q is not a scheduled turn of this chat", scheduleID)
	}
	inspector := coderScheduleInspector()
	if inspector == nil {
		return false, fmt.Errorf("coder_schedule is unavailable: this process published no queue handles")
	}
	info, err := inspector.GetTaskInfo(workqueue.QueueDefault, scheduleID)
	if err != nil {
		// A task that already ran or was already cancelled is reported as "not
		// found"; the tool answers "nothing pending" instead of failing.
		return false, nil
	}
	if info == nil {
		return false, nil
	}
	if info.Type != workqueue.TypeBotReply {
		return false, fmt.Errorf("%q is not a scheduled chat turn", scheduleID)
	}
	if info.State == asynq.TaskStateActive {
		return false, fmt.Errorf("the scheduled turn %q has already started and can no longer be cancelled", scheduleID)
	}
	if err := inspector.DeleteTask(workqueue.QueueDefault, scheduleID); err != nil {
		return false, nil
	}
	return true, nil
}

// coderScheduleTaskID builds the task ID of a scheduled turn. It always carries
// the chat UUID (so cancellation can prove ownership) and a nonce (so two
// schedules of the same chat never collide); the caller's correlation ID is
// appended as a readable hint.
func coderScheduleTaskID(chatUUID string, correlationID string) string {
	scheduleID := coderScheduleTaskPrefix + chatUUID + ":" + coderScheduleNonce()
	if correlationID = strings.TrimSpace(correlationID); correlationID != "" {
		scheduleID += ":" + correlationID
	}
	return scheduleID
}

func coderScheduleNonce() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("%x", coderScheduleNow().UnixNano())
}

// coderScheduleParticipants splits the chat participants into the automated
// (bot) side and the user side.
func coderScheduleParticipants(chat database.Chat) (*database.User, *database.User) {
	switch {
	case chat.User1.IsAutomated && !chat.User2.IsAutomated:
		return &chat.User1, &chat.User2
	case chat.User2.IsAutomated && !chat.User1.IsAutomated:
		return &chat.User2, &chat.User1
	default:
		return nil, nil
	}
}

// coderScheduleToolRunner is the provider the tools package calls. It returns a
// zero-value runner; the runner itself resolves the process handles lazily, so a
// deployment without them fails loudly at call time instead of at init.
func coderScheduleToolRunner() tooldefs.CoderScheduleRunner {
	return coderScheduleRunner{}
}
