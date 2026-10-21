package msgmate

import (
	"fmt"
	"strings"
	"sync"

	tooldefs "backend/api/msgmate/tools"
)

// coderTodoRegistry is the core-side home of the ci#394 task lists: one list per
// chat, keyed by chat UUID. It backs the `coder_todo_write` / `coder_todo_read`
// tools through the tooldefs.CoderTodoStoreProvider seam (the tools package
// cannot import this package).
//
// The registry is process-local on purpose: the list is turn-over-progress state
// an agent uses to plan and to re-read its own plan after a continuation, exactly
// like the context-usage registry next to it. Persisting the list as chat data is
// a separate step and deliberately not done here - no user content is written to
// the database by a tool call.
type coderTodoRegistry struct {
	mu    sync.RWMutex
	lists map[string][]tooldefs.CoderTodoItem
}

var coderTodos = newCoderTodoRegistry()

func newCoderTodoRegistry() *coderTodoRegistry {
	return &coderTodoRegistry{lists: map[string][]tooldefs.CoderTodoItem{}}
}

// ReadCoderTodos returns a copy of the chat's list (empty, never nil).
func (r *coderTodoRegistry) ReadCoderTodos(chatUUID string) ([]tooldefs.CoderTodoItem, error) {
	chatUUID = strings.TrimSpace(chatUUID)
	if chatUUID == "" {
		return nil, fmt.Errorf("the chat task list needs a chat UUID")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.lists[chatUUID]
	out := make([]tooldefs.CoderTodoItem, 0, len(items))
	return append(out, items...), nil
}

// WriteCoderTodos replaces the chat's list with a copy of items.
func (r *coderTodoRegistry) WriteCoderTodos(chatUUID string, items []tooldefs.CoderTodoItem) ([]tooldefs.CoderTodoItem, error) {
	chatUUID = strings.TrimSpace(chatUUID)
	if chatUUID == "" {
		return nil, fmt.Errorf("the chat task list needs a chat UUID")
	}
	stored := make([]tooldefs.CoderTodoItem, 0, len(items))
	stored = append(stored, items...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(stored) == 0 {
		// An empty write clears the plan instead of pinning an empty slice, so
		// the registry does not grow one entry per chat that ever wrote a list.
		delete(r.lists, chatUUID)
		return []tooldefs.CoderTodoItem{}, nil
	}
	r.lists[chatUUID] = stored
	return append([]tooldefs.CoderTodoItem{}, stored...), nil
}

// resetCoderTodos clears the registry. It exists for tests, which must not leak
// one chat's plan into the next test.
func resetCoderTodos() {
	coderTodos.mu.Lock()
	defer coderTodos.mu.Unlock()
	coderTodos.lists = map[string][]tooldefs.CoderTodoItem{}
}

// coderTodoToolStore is the provider the tools package calls.
func coderTodoToolStore() tooldefs.CoderTodoStore {
	return coderTodos
}
