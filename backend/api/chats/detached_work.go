package chats

import "backend/database"

// chatsDetachedDBWork tracks the goroutines the chats handlers detach from their
// HTTP response but which keep using the database pool.
//
// The handlers in this package are best-effort by design: the
// interaction-confirmed/completed notifications and the auto-continuation follow-up
// must not delay the response, so they run in a goroutine. That goroutine, however,
// still writes through the *gorm.DB it was handed. In a test the pool is closed and
// t.TempDir removes the directory as soon as the test body returns, so a detached
// write could land after the close (or reopen the SQLite file) and turn the
// teardown into "TempDir RemoveAll cleanup: unlinkat ...: directory not empty"
// (ci#811, the ci#920 guard only looked at the pool).
//
// Routing that work through database.DetachedWork makes the handshake explicit:
// the group is registered with the shared test helpers, so the teardown drains it
// before CloseTestDB closes the pool. Any goroutine in this package that is handed
// a DB handle must go through it - see the guard test in backend/database.
var chatsDetachedDBWork = database.RegisterDetachedWork(&database.DetachedWork{})

// GoDetachedDBWork runs fn in a goroutine tracked by chatsDetachedDBWork. Other
// packages that hand this package a pool for a detached notification have to use
// it instead of a bare `go` statement, so the ci#811 test teardown still drains
// the write before CloseTestDB closes the pool.
func GoDetachedDBWork(fn func()) {
	chatsDetachedDBWork.Go(fn)
}
