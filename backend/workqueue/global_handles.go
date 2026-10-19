package workqueue

import (
	"sync"

	"github.com/hibiken/asynq"
)

// ci#724: an in-product tool that schedules work for a later turn (eg
// `coder_schedule`) has no request scope, so it cannot borrow the
// request-scoped asynq client/inspector the HTTP handlers build per call. The
// process that owns the queue publishes its handles once at startup with
// SetGlobalQueueHandles, mirroring database.SetGlobalDB.
var (
	globalHandlesMu sync.RWMutex
	globalClient    *asynq.Client
	globalInspector *asynq.Inspector
)

// SetGlobalQueueHandles publishes the process-wide asynq handles. A nil
// argument leaves the other handle untouched, so a caller may publish only what
// it owns.
func SetGlobalQueueHandles(client *asynq.Client, inspector *asynq.Inspector) {
	globalHandlesMu.Lock()
	defer globalHandlesMu.Unlock()
	if client != nil {
		globalClient = client
	}
	if inspector != nil {
		globalInspector = inspector
	}
}

// GetGlobalQueueHandles returns the process-wide asynq handles, or nils when no
// queue-owning process published them (a tool must then fail explicitly instead
// of silently dropping scheduled work).
func GetGlobalQueueHandles() (*asynq.Client, *asynq.Inspector) {
	globalHandlesMu.RLock()
	defer globalHandlesMu.RUnlock()
	return globalClient, globalInspector
}
