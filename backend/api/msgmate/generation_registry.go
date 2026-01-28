package msgmate

import (
	"context"
	"sync"
)

// activeChatGeneration is a handle to one in-flight native generation for a
// chat. The cancel func aborts the provider stream and the tool loop; the
// pointer identity lets a finishing generation only remove itself, never a
// newer generation that superseded it.
type activeChatGeneration struct {
	cancel context.CancelFunc
}

var activeChatGenerations = struct {
	mu     sync.Mutex
	byChat map[string]*activeChatGeneration
}{
	byChat: map[string]*activeChatGeneration{},
}

// TrackActiveChatGeneration records the cancel func of a chat's in-flight
// native generation and returns a release func that deregisters it. A newer
// generation for the same chat supersedes (and cancels) the previous one so a
// chat never has two live generations racing to write the same transcript.
func TrackActiveChatGeneration(chatUUID string, cancel context.CancelFunc) func() {
	if chatUUID == "" || cancel == nil {
		return func() {}
	}

	generation := &activeChatGeneration{cancel: cancel}

	activeChatGenerations.mu.Lock()
	previous := activeChatGenerations.byChat[chatUUID]
	activeChatGenerations.byChat[chatUUID] = generation
	activeChatGenerations.mu.Unlock()

	if previous != nil && previous.cancel != nil {
		previous.cancel()
	}

	return func() {
		activeChatGenerations.mu.Lock()
		if activeChatGenerations.byChat[chatUUID] == generation {
			delete(activeChatGenerations.byChat, chatUUID)
		}
		activeChatGenerations.mu.Unlock()
	}
}

// HasActiveChatGeneration reports whether a native generation is currently
// in flight for the chat. It backs the chat-state provider so the chat list
// shows "running" during long native turns.
func HasActiveChatGeneration(chatUUID string) bool {
	if chatUUID == "" {
		return false
	}
	activeChatGenerations.mu.Lock()
	defer activeChatGenerations.mu.Unlock()
	return activeChatGenerations.byChat[chatUUID] != nil
}

// CancelActiveChatGeneration aborts the chat's in-flight native generation and
// reports whether one was cancelled. It is the native chat backend interrupt
// handler reachable from the chat interrupt signal endpoint.
func CancelActiveChatGeneration(chatUUID string) bool {
	if chatUUID == "" {
		return false
	}
	activeChatGenerations.mu.Lock()
	generation := activeChatGenerations.byChat[chatUUID]
	activeChatGenerations.mu.Unlock()

	if generation == nil || generation.cancel == nil {
		return false
	}
	generation.cancel()
	return true
}
