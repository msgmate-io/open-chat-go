package database

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestDetachedWorkWaitsForTrackedGoroutines pins the ci#811 handshake: Wait returns
// only once every tracked goroutine has returned, and reports the ones that did not
// instead of hanging or claiming success.
func TestDetachedWorkWaitsForTrackedGoroutines(t *testing.T) {
	var group DetachedWork

	release := make(chan struct{})
	started := make(chan struct{})
	var ran atomic.Bool
	group.Go(func() {
		close(started)
		<-release
		ran.Store(true)
	})
	<-started

	if group.Pending() != 1 || group.Started() != 1 {
		t.Fatalf("expected one tracked goroutine, got pending=%d started=%d", group.Pending(), group.Started())
	}
	if group.Wait(20 * time.Millisecond) {
		t.Fatal("a bounded Wait must report a tracked goroutine that is still running, not claim it finished")
	}
	close(release)
	if !group.Wait(5 * time.Second) {
		t.Fatal("Wait must return once the tracked goroutine finished")
	}
	if !ran.Load() || group.Pending() != 0 {
		t.Fatalf("expected the tracked goroutine to have run, got ran=%v pending=%d", ran.Load(), group.Pending())
	}
	if !group.Wait(time.Millisecond) {
		t.Fatal("Wait on a quiescent group must return immediately")
	}
}

// TestDetachedWorkRegistryDrainsRegisteredGroups pins that a package can hand its
// group to the shared teardown, which is what makes the generic CloseTestDB path
// drain it - work nothing drains is the ci#811 bug with extra steps.
func TestDetachedWorkRegistryDrainsRegisteredGroups(t *testing.T) {
	group := RegisterDetachedWork(&DetachedWork{})
	if group == nil {
		t.Fatal("RegisterDetachedWork must return the group it registered")
	}
	if again := RegisterDetachedWork(group); again != group {
		t.Fatal("re-registering the same group must be a no-op")
	}

	finished := make(chan struct{})
	group.Go(func() { close(finished) })
	if !DrainRegisteredDetachedWork(5 * time.Second) {
		t.Fatal("DrainRegisteredDetachedWork must report success once the groups are quiescent")
	}
	select {
	case <-finished:
	default:
		t.Fatal("DrainRegisteredDetachedWork returned before the tracked goroutine finished")
	}
	if !DrainRegisteredDetachedWork(time.Millisecond) {
		t.Fatal("draining a quiescent registry must succeed immediately")
	}
}
