package database

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

func sessionCountForToken(t *testing.T, db *gorm.DB, token string) int64 {
	t.Helper()
	var count int64
	if err := db.Unscoped().Model(&Session{}).Where("token = ?", token).Count(&count).Error; err != nil {
		t.Fatalf("failed to count sessions: %v", err)
	}
	return count
}

// TestDeleteExpiredSessionsRemovesOnlyStaleRows pins the ci#729 follow-up: the
// auth middleware rejects an expired session but never removes it, so without a
// sweep the sessions table grows forever on the SQLite volume.
func TestDeleteExpiredSessionsRemovesOnlyStaleRows(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	now := time.Now().UTC()

	createSessionForTest(t, db, "stale-token", now.Add(-48*time.Hour))
	createSessionForTest(t, db, "within-grace-token", now.Add(-30*time.Minute))
	createSessionForTest(t, db, "fresh-token", now.Add(24*time.Hour))

	deleted, err := DeleteExpiredSessions(context.Background(), db, now)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions returned an error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected exactly the stale session to be deleted, got %d", deleted)
	}

	if count := sessionCountForToken(t, db, "stale-token"); count != 0 {
		t.Fatalf("stale session should be gone (unscoped count %d), a soft delete would leave it behind", count)
	}
	if count := sessionCountForToken(t, db, "within-grace-token"); count != 1 {
		t.Fatalf("session inside the grace window must survive, got %d rows", count)
	}
	if count := sessionCountForToken(t, db, "fresh-token"); count != 1 {
		t.Fatalf("unexpired session must survive, got %d rows", count)
	}
}

// TestDeleteExpiredSessionsIsIdempotent keeps the sweep safe to run from more
// than one process (the server plus an ad-hoc maintenance run).
func TestDeleteExpiredSessionsIsIdempotent(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	now := time.Now().UTC()

	createSessionForTest(t, db, "stale-token", now.Add(-72*time.Hour))

	first, err := DeleteExpiredSessions(context.Background(), db, now)
	if err != nil {
		t.Fatalf("first sweep failed: %v", err)
	}
	if first != 1 {
		t.Fatalf("expected the stale session to be deleted once, got %d", first)
	}

	second, err := DeleteExpiredSessions(context.Background(), db, now)
	if err != nil {
		t.Fatalf("second sweep failed: %v", err)
	}
	if second != 0 {
		t.Fatalf("a repeated sweep must not delete anything, got %d", second)
	}
}

func TestDeleteExpiredSessionsRejectsNilDB(t *testing.T) {
	if _, err := DeleteExpiredSessions(context.Background(), nil, time.Now()); err == nil {
		t.Fatal("expected an error when no db handle is provided")
	}
}

// TestDeleteExpiredSessionsHonoursCancelledContext guards the shutdown path: a
// cancelled context must abort instead of writing.
func TestDeleteExpiredSessionsHonoursCancelledContext(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	createSessionForTest(t, db, "stale-token", time.Now().UTC().Add(-72*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := DeleteExpiredSessions(ctx, db, time.Now()); err == nil {
		t.Fatal("expected the cancelled context to abort the sweep")
	}
}

// TestSessionSweepInterval pins the knob: unset and unparsable values fall back
// to the default, while the documented off-switches (and non-positive
// durations) disable the sweeper.
func TestSessionSweepInterval(t *testing.T) {
	cases := []struct {
		value string
		want  time.Duration
	}{
		{value: "", want: DefaultSessionSweepInterval},
		{value: "15m", want: 15 * time.Minute},
		{value: "2h30m", want: 2*time.Hour + 30*time.Minute},
		{value: "off", want: 0},
		{value: "OFF", want: 0},
		{value: "disabled", want: 0},
		{value: "never", want: 0},
		{value: "0", want: 0},
		{value: "-5m", want: 0},
		{value: "not-a-duration", want: DefaultSessionSweepInterval},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(SessionSweepIntervalEnv, tc.value)
			if got := SessionSweepInterval(); got != tc.want {
				t.Fatalf("SessionSweepInterval() with %q = %s, want %s", tc.value, got, tc.want)
			}
		})
	}
}

// TestStartSessionSweeperSweepsAndStops checks the background loop end to end:
// it deletes a stale row on its own, and its stop function is blocking so no
// sweep can outlive the caller (and the test leaks no goroutine).
func TestStartSessionSweeperSweepsAndStops(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	createSessionForTest(t, db, "stale-token", time.Now().UTC().Add(-48*time.Hour))

	stop := StartSessionSweeper(db, 5*time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	for sessionCountForToken(t, db, "stale-token") != 0 {
		if time.Now().After(deadline) {
			stop()
			t.Fatal("the sweeper never removed the stale session")
		}
		time.Sleep(5 * time.Millisecond)
	}

	stop()
	stop() // idempotent

	// A second, expired row created after the stop must stay: the loop is gone.
	createSessionForTest(t, db, "post-stop-token", time.Now().UTC().Add(-48*time.Hour))
	time.Sleep(50 * time.Millisecond)
	if count := sessionCountForToken(t, db, "post-stop-token"); count != 1 {
		t.Fatalf("the sweeper kept running after stop (unscoped count %d)", count)
	}
}

// TestStartSessionSweeperDisabled keeps a disabled sweeper a no-op so the server
// can call it unconditionally.
func TestStartSessionSweeperDisabled(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	createSessionForTest(t, db, "stale-token", time.Now().UTC().Add(-48*time.Hour))

	StartSessionSweeper(db, 0)()
	StartSessionSweeper(nil, time.Hour)()

	if count := sessionCountForToken(t, db, "stale-token"); count != 1 {
		t.Fatalf("a disabled sweeper must not delete anything, got %d rows", count)
	}
}
