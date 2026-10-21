package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// coder_todo_write / coder_todo_read (ci#394 gap 1: todo / task list).
//
// opencode's todowrite/todoread give the agent a structured, visible task list:
// the plan is written once and the statuses are updated while the work happens,
// instead of restating a prose plan in every message. The two tools below mirror
// that with the same semantics as todowrite: a write *replaces* the whole list.
//
// The list is owned by the deployment, not by this package: the tools package
// cannot import the core package it is embedded in, so the owning process
// publishes its store through CoderTodoStoreProvider - the same indirection
// CoderScheduleRunnerProvider and ContextUsageLookup use. A nil provider makes
// the tools fail loudly instead of silently dropping the plan.
//
// A list is scoped to *one* chat (the chat of the running turn, taken from the
// trusted tool init), so a model can never rewrite the plan of another chat.

// CoderTodoStatus is the lifecycle state of a todo item. Only these three values
// are accepted, so a model cannot invent a state the UI (or the agent prompt)
// cannot render.
const (
	CoderTodoStatusPending    = "pending"
	CoderTodoStatusInProgress = "in_progress"
	CoderTodoStatusCompleted  = "completed"
)

// CoderTodoItem is one entry of the task list.
type CoderTodoItem struct {
	// Content is the imperative description ("add the parity test").
	Content string `json:"content"`
	// Status is one of CoderTodoStatus* (defaults to pending).
	Status string `json:"status"`
	// ActiveForm is the present-continuous form ("adding the parity test") the
	// UI shows while the item is in progress. Optional.
	ActiveForm string `json:"active_form,omitempty"`
}

// CoderTodoStore is the deployment-side home of a chat's todo list.
type CoderTodoStore interface {
	// ReadCoderTodos returns the current list of a chat (empty, never nil, when
	// no list was written yet).
	ReadCoderTodos(chatUUID string) ([]CoderTodoItem, error)
	// WriteCoderTodos replaces the list of a chat and returns the stored list.
	WriteCoderTodos(chatUUID string, items []CoderTodoItem) ([]CoderTodoItem, error)
}

// CoderTodoStoreProvider returns the store of the current deployment, or nil
// when no core published one.
var CoderTodoStoreProvider func() CoderTodoStore

// CoderTodoWriteInput is the replacement list of coder_todo_write.
type CoderTodoWriteInput struct {
	Todos []CoderTodoItem `json:"todos"`
}

// CoderTodoReadInput carries no fields: the list always belongs to the trusted
// chat of the running turn.
type CoderTodoReadInput struct{}

type coderTodoResult struct {
	ChatUUID   string          `json:"chat_uuid"`
	Count      int             `json:"count"`
	Done       int             `json:"completed"`
	InProgress int             `json:"in_progress"`
	Todos      []CoderTodoItem `json:"todos"`
	Note       string          `json:"note"`
}

var CoderTodoWriteToolDef = ToolDefinition{
	Name:        "coder_todo_write",
	Description: "Write the task list of this chat (replaces the previous one). Use it for any multi-step task: write the plan up front, then call it again with updated statuses as you go (one item in_progress at a time, completed as soon as it is done). Statuses are pending, in_progress and completed; content is the imperative form and active_form the present-continuous form shown while working. This keeps your progress visible without restating the plan in prose.",
	InputType:   CoderTodoWriteInput{},
	// RequiresInit: the list belongs to the trusted chat of the running turn.
	RequiresInit:   true,
	RequiredParams: []string{"todos"},
	Parameters: map[string]interface{}{
		"todos": map[string]interface{}{
			"type":        "array",
			"description": "The complete task list; it replaces the previous one (pass an empty array to clear it).",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content":     map[string]interface{}{"type": "string", "description": "The task in imperative form."},
					"status":      map[string]interface{}{"type": "string", "enum": []string{CoderTodoStatusPending, CoderTodoStatusInProgress, CoderTodoStatusCompleted}},
					"active_form": map[string]interface{}{"type": "string", "description": "Present-continuous form shown while the task is in progress."},
				},
				"required": []string{"content", "status"},
			},
		},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		var store CoderTodoStore
		if CoderTodoStoreProvider != nil {
			store = CoderTodoStoreProvider()
		}
		return runCoderTodoWrite(input, init, store)
	},
}

var CoderTodoReadToolDef = ToolDefinition{
	Name:        "coder_todo_read",
	Description: "Read the task list of this chat, including the status of every item. Call it after a long stretch of work (or a continuation) to re-check what is still pending instead of trusting your memory of the plan.",
	InputType:   CoderTodoReadInput{},
	// RequiresInit: the list belongs to the trusted chat of the running turn.
	RequiresInit:   true,
	RequiredParams: []string{},
	Parameters:     map[string]interface{}{},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		var store CoderTodoStore
		if CoderTodoStoreProvider != nil {
			store = CoderTodoStoreProvider()
		}
		return runCoderTodoRead(input, init, store)
	},
}

// NormalizeCoderTodos validates and normalizes a model-written list: content is
// required, a blank or unknown status becomes pending, an unknown status is not
// an error (the item itself is still worth keeping) and a missing active_form
// stays empty. It returns the normalized copy so a caller never mutates its
// input.
func NormalizeCoderTodos(items []CoderTodoItem) ([]CoderTodoItem, error) {
	out := make([]CoderTodoItem, 0, len(items))
	seen := map[string]bool{}
	for index, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return nil, fmt.Errorf("todo item %d has no content", index+1)
		}
		key := strings.ToLower(content)
		if seen[key] {
			return nil, fmt.Errorf("todo item %d duplicates %q", index+1, content)
		}
		seen[key] = true
		status := strings.ToLower(strings.TrimSpace(item.Status))
		switch status {
		case CoderTodoStatusPending, CoderTodoStatusInProgress, CoderTodoStatusCompleted:
		default:
			status = CoderTodoStatusPending
		}
		out = append(out, CoderTodoItem{
			Content:    content,
			Status:     status,
			ActiveForm: strings.TrimSpace(item.ActiveForm),
		})
	}
	return out, nil
}

// CoderTodoCounts reports how many items are completed and in progress, so a
// caller can render progress without walking the list itself.
func CoderTodoCounts(items []CoderTodoItem) (completed int, inProgress int) {
	for _, item := range items {
		switch item.Status {
		case CoderTodoStatusCompleted:
			completed++
		case CoderTodoStatusInProgress:
			inProgress++
		}
	}
	return completed, inProgress
}

// runCoderTodoWrite is the testable core of coder_todo_write.
func runCoderTodoWrite(input interface{}, init map[string]interface{}, store CoderTodoStore) (string, error) {
	in, _ := input.(CoderTodoWriteInput)
	if pointer, ok := input.(*CoderTodoWriteInput); ok && pointer != nil {
		in = *pointer
	}
	chatUUID := strings.TrimSpace(chatUUIDFromInit(init))
	if chatUUID == "" {
		return "", fmt.Errorf("coder_todo_write needs the chat identity of the running turn")
	}
	items, err := NormalizeCoderTodos(in.Todos)
	if err != nil {
		return "", err
	}
	if store == nil {
		return "", fmt.Errorf("the chat task list is unavailable in this deployment")
	}
	stored, err := store.WriteCoderTodos(chatUUID, items)
	if err != nil {
		return "", err
	}
	return encodeCoderTodoResult(chatUUID, stored, fmt.Sprintf(
		"The task list of this chat now has %d item(s); re-write it after each step instead of narrating the plan.",
		len(stored)))
}

// runCoderTodoRead is the testable core of coder_todo_read.
func runCoderTodoRead(input interface{}, init map[string]interface{}, store CoderTodoStore) (string, error) {
	chatUUID := strings.TrimSpace(chatUUIDFromInit(init))
	if chatUUID == "" {
		return "", fmt.Errorf("coder_todo_read needs the chat identity of the running turn")
	}
	if store == nil {
		return "", fmt.Errorf("the chat task list is unavailable in this deployment")
	}
	items, err := store.ReadCoderTodos(chatUUID)
	if err != nil {
		return "", err
	}
	note := "The task list of this chat is empty: no plan was written for it yet."
	if len(items) > 0 {
		note = "This is the current plan of the chat. Update it with coder_todo_write as the statuses change."
	}
	return encodeCoderTodoResult(chatUUID, items, note)
}

func encodeCoderTodoResult(chatUUID string, items []CoderTodoItem, note string) (string, error) {
	completed, inProgress := CoderTodoCounts(items)
	result := coderTodoResult{
		ChatUUID:   chatUUID,
		Count:      len(items),
		Done:       completed,
		InProgress: inProgress,
		Todos:      items,
		Note:       note,
	}
	if result.Todos == nil {
		result.Todos = []CoderTodoItem{}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
