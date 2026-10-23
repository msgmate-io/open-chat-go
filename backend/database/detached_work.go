package database

import (
	"sync"
	"time"
)

// DetachedWork tracks the background goroutines that were handed a database
// handle, so a test can wait for them *before* the pool is closed and t.TempDir
// removes the directory (ci#811).
//
// Waiting for the pool to be quiescent is not enough on its own: a goroutine that
// is between two statements holds no connection at the moment it is checked, so
// only an explicit handshake proves that no further write can arrive. Handlers
// that deliberately detach work from their HTTP response therefore hand that work
// to a group here, and the test teardown waits on the group before the pool is
// closed (`backend/api/chats` keeps one package-level group, registers it with
// RegisterDetachedWork and drains it in its close*TestDB helper, ci#811).
//
// The zero value is ready to use, and Go/Started/Pending/Wait are safe for
// concurrent callers.
type DetachedWork struct {
	mu       sync.Mutex
	inflight int
	started  int
	idle     chan struct{}
}

// Go starts fn on a tracked goroutine. Callers must not block on the goroutine;
// the group is drained by the test teardown (Wait) instead.
func (w *DetachedWork) Go(fn func()) {
	if w == nil {
		go fn()
		return
	}
	if fn == nil {
		return
	}

	w.mu.Lock()
	if w.inflight == 0 {
		w.idle = make(chan struct{})
	}
	w.inflight++
	w.started++
	w.mu.Unlock()

	go func() {
		defer w.done()
		fn()
	}()
}

func (w *DetachedWork) done() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inflight--
	if w.inflight <= 0 {
		w.inflight = 0
		if w.idle != nil {
			close(w.idle)
			w.idle = nil
		}
	}
}

// Started reports how many goroutines have been handed to this group since the
// process started. It is monotonic (unlike Pending) and exists so a test can
// assert that a handler still routes its detached work through the group instead
// of spawning a bare goroutine (ci#811).
func (w *DetachedWork) Started() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started
}

// Pending reports how many tracked goroutines have not returned yet.
func (w *DetachedWork) Pending() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inflight
}

// Wait blocks until every goroutine started through Go has returned and reports
// whether the group went quiescent. A non-positive timeout waits without bound; a
// positive one is an upper bound, so a goroutine that never returns is reported by
// the caller instead of hanging the test binary.
func (w *DetachedWork) Wait(timeout time.Duration) bool {
	if w == nil {
		return true
	}

	w.mu.Lock()
	if w.inflight == 0 {
		w.mu.Unlock()
		return true
	}
	idle := w.idle
	w.mu.Unlock()

	if timeout <= 0 {
		<-idle
		return true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-idle:
		return true
	case <-timer.C:
		return false
	}
}

// registeredDetachedWork holds every group a package exposed with
// RegisterDetachedWork, in registration order.
var registeredDetachedWork = struct {
	mu     sync.Mutex
	groups []*DetachedWork
}{}

// RegisterDetachedWork makes a package-level group drainable by the shared test
// helpers (CloseTestDB/SetupTestDatabase) and returns it, so a group can be
// declared as
//
//	var chatsDetachedDBWork = database.RegisterDetachedWork(&database.DetachedWork{})
//
// A nil group is returned unchanged. Registering the same group twice is a no-op.
// Registration is what makes the generic teardown path safe: a package that only
// remembers to drain in its own helper loses that guarantee the moment a test
// pairs the pool with a goroutine by another route.
func RegisterDetachedWork(w *DetachedWork) *DetachedWork {
	if w == nil {
		return nil
	}
	registeredDetachedWork.mu.Lock()
	defer registeredDetachedWork.mu.Unlock()
	for _, existing := range registeredDetachedWork.groups {
		if existing == w {
			return w
		}
	}
	registeredDetachedWork.groups = append(registeredDetachedWork.groups, w)
	return w
}

// DrainRegisteredDetachedWork waits (bounded by timeout, <= 0 waits without bound)
// until every group registered with RegisterDetachedWork is quiescent and reports
// whether that succeeded. The pool close in CloseTestDB calls it first (ci#811).
func DrainRegisteredDetachedWork(timeout time.Duration) bool {
	registeredDetachedWork.mu.Lock()
	groups := make([]*DetachedWork, len(registeredDetachedWork.groups))
	copy(groups, registeredDetachedWork.groups)
	registeredDetachedWork.mu.Unlock()

	deadline := time.Now().Add(timeout)
	quiescent := true
	for _, group := range groups {
		remaining := timeout
		if timeout > 0 {
			remaining = time.Until(deadline)
			if remaining <= 0 {
				quiescent = false
				break
			}
		}
		if !group.Wait(remaining) {
			quiescent = false
		}
	}
	return quiescent
}
