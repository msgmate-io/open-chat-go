package chats

import (
	"backend/database"
	"encoding/json"
	"testing"
)

// ci#1100: the chat details "Todos" tab renders the todo state the coder agent
// maintains for an interaction run. It rides the existing status poll as an
// additive `todos` field instead of a new route, and is derived per run from the
// newest `todo_write` tool call of the chat's own transcript.

func todoCalls(t *testing.T, raw string) *[]json.RawMessage {
	t.Helper()
	var calls []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &calls); err != nil {
		t.Fatalf("unmarshal tool calls: %v", err)
	}
	return &calls
}

func TestTodosFromMessagesReadsNewestTodoWrite(t *testing.T) {
	messages := []database.Message{
		{
			ToolCalls: todoCalls(t, `[
				{"id":"call-2","name":"todo_write","arguments":{"todos":[
					{"text":"ship the tab","status":"in_progress"},
					{"text":"open the PR","status":"pending"}
				]}}
			]`),
		},
		{
			ToolCalls: todoCalls(t, `[
				{"id":"call-1","name":"todo_write","arguments":{"todos":[
					{"text":"old plan","status":"completed"}
				]}}
			]`),
		},
	}

	todos, ok := todosFromMessages(messages)
	if !ok {
		t.Fatalf("expected a todo_write call to be found")
	}
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %#v", todos)
	}
	if todos[0].Text != "ship the tab" || todos[0].Status != "in_progress" {
		t.Fatalf("unexpected first todo: %#v", todos[0])
	}
	if todos[1].Text != "open the PR" || todos[1].Status != "pending" {
		t.Fatalf("unexpected second todo: %#v", todos[1])
	}
}

func TestTodosFromMessagesAcceptsNestedFunctionShape(t *testing.T) {
	messages := []database.Message{
		{
			ToolCalls: todoCalls(t, `[
				{"id":"call-1","function":{
					"name":"coder_todo_write",
					"arguments":"{\"todos\":[{\"text\":\" parse the string args \",\"status\":\"done\"},{\"content\":\"content fallback\",\"status\":\"in-progress\"}]}"
				}}
			]`),
		},
	}

	todos, ok := todosFromMessages(messages)
	if !ok {
		t.Fatalf("expected the nested function call to be understood")
	}
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %#v", todos)
	}
	if todos[0].Text != "parse the string args" || todos[0].Status != "completed" {
		t.Fatalf("unexpected first todo: %#v", todos[0])
	}
	if todos[1].Text != "content fallback" || todos[1].Status != "in_progress" {
		t.Fatalf("unexpected second todo: %#v", todos[1])
	}
}

func TestTodosFromMessagesReportsMissingList(t *testing.T) {
	messages := []database.Message{
		{ToolCalls: todoCalls(t, `[{"id":"call-1","name":"coder_read","arguments":{"path":"main.go"}}]`)},
		{ToolCalls: todoCalls(t, `[{"id":"call-2","name":"todo_write","arguments":{}}]`)},
		{},
	}

	if todos, ok := todosFromMessages(messages); ok || todos != nil {
		t.Fatalf("expected no todo list, got %#v/%v", todos, ok)
	}
}

func TestTodosFromMessagesKeepsEmptyListDistinctFromAbsentList(t *testing.T) {
	messages := []database.Message{
		{ToolCalls: todoCalls(t, `[{"id":"call-1","name":"todo_write","arguments":{"todos":[]}}]`)},
	}

	todos, ok := todosFromMessages(messages)
	if !ok {
		t.Fatalf("an empty todo list is still an answer from the agent")
	}
	if len(todos) != 0 {
		t.Fatalf("expected an empty list, got %#v", todos)
	}
}

func TestLatestTodoStateReusesTranscriptTail(t *testing.T) {
	tail := []database.Message{
		{ToolCalls: todoCalls(t, `[{"id":"call-1","name":"todo_write","arguments":{"todos":[{"text":"only step","status":"pending"}]}}]`)},
	}

	// A nil DB proves no extra query is issued when the tail already answers.
	todos := latestTodoState(nil, 1, tail)
	if len(todos) != 1 || todos[0].Text != "only step" {
		t.Fatalf("expected the tail todo, got %#v", todos)
	}
}

func TestInteractionStatusResponseSerializesTodosAdditively(t *testing.T) {
	encoded, err := json.Marshal(InteractionStatusResponse{
		ChatUUID: "chat-1",
		State:    "active",
		Todos: []InteractionStatusTodo{
			{Text: "ship the tab", Status: "in_progress"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rawTodos, ok := decoded["todos"].([]interface{})
	if !ok || len(rawTodos) != 1 {
		t.Fatalf("expected a todos array in the payload, got %#v", decoded["todos"])
	}
	first, _ := rawTodos[0].(map[string]interface{})
	if first["text"] != "ship the tab" || first["status"] != "in_progress" {
		t.Fatalf("unexpected serialized todo: %#v", first)
	}

	// The field stays additive: chats without a todo list serialize exactly as
	// before, ie without the key at all.
	empty, err := json.Marshal(InteractionStatusResponse{ChatUUID: "chat-2", State: "active"})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	var emptyDecoded map[string]interface{}
	if err := json.Unmarshal(empty, &emptyDecoded); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if _, present := emptyDecoded["todos"]; present {
		t.Fatalf("expected an absent todos key for a chat without todos: %s", empty)
	}
}

func TestNormalizeTodoStatus(t *testing.T) {
	cases := map[string]string{
		"pending":     "pending",
		"in_progress": "in_progress",
		"in-progress": "in_progress",
		" Done ":      "completed",
		"cancelled":   "cancelled",
		"canceled":    "cancelled",
		"":            "pending",
		"whatever":    "pending",
	}
	for input, want := range cases {
		if got := normalizeTodoStatus(input); got != want {
			t.Fatalf("normalizeTodoStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsTodoWriteToolName(t *testing.T) {
	for _, name := range []string{"todo_write", "Todo_Write", "coder_todo_write", " coder_todo_write "} {
		if !isTodoWriteToolName(name) {
			t.Fatalf("expected %q to be recognized as todo_write", name)
		}
	}
	for _, name := range []string{"", "todo_read", "update_plan", "coder_read"} {
		if isTodoWriteToolName(name) {
			t.Fatalf("did not expect %q to be recognized as todo_write", name)
		}
	}
}
