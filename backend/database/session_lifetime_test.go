package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

func setupSessionLifetimeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := SetupDatabase(DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "session_lifetime_test.db"),
		ResetDB:  true,
	})
	if db == nil {
		t.Fatal("failed to set up test database")
	}
	return db
}

func createSessionForTest(t *testing.T, db *gorm.DB, token string, expiry time.Time) {
	t.Helper()
	user, err := RegisterUser(db, RandomUsername(), RandomUsername()+"@example.com", []byte("Passw0rd!"))
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}
	if err := db.Create(&Session{UserId: user.ID, Token: token, Expiry: expiry}).Error; err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
}

func sessionExpiryForToken(t *testing.T, db *gorm.DB, token string) time.Time {
	t.Helper()
	var session Session
	if err := db.First(&session, "token = ?", token).Error; err != nil {
		t.Fatalf("failed to load session: %v", err)
	}
	return session.Expiry
}

// TestSessionTTLCoversTurnBudget pins the fix for ci#729: the session behind an
// automated turn must outlive the turn budget, otherwise a turn that runs longer
// than the old fixed 15 minutes gets 403 "Invalid token" on every tool call.
func TestSessionTTLCoversTurnBudget(t *testing.T) {
	t.Setenv("OPEN_CHAT_BOT_REPLY_TIMEOUT", "30m")

	cases := []struct {
		name        string
		turnBudget  time.Duration
		wantAtLeast time.Duration
	}{
		{name: "default 30 minute turn budget", turnBudget: 30 * time.Minute, wantAtLeast: 30 * time.Minute},
		{name: "hour long turn budget", turnBudget: time.Hour, wantAtLeast: time.Hour},
		{name: "tiny budget never drops below the floor", turnBudget: time.Second, wantAtLeast: MinBotSessionTTL},
		{name: "unset budget falls back to the turn timeout", turnBudget: 0, wantAtLeast: 30 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ttl := SessionTTL(tc.turnBudget)
			if ttl < tc.wantAtLeast {
				t.Fatalf("SessionTTL(%v) = %v, want at least %v", tc.turnBudget, ttl, tc.wantAtLeast)
			}
			if ttl <= 15*time.Minute {
				t.Fatalf("session TTL must exceed the old fixed 15m limit, got %v", ttl)
			}
		})
	}
}

func TestSessionRefreshIntervalStaysBelowTTL(t *testing.T) {
	cases := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{name: "short ttl uses the busy-loop floor", ttl: 90 * time.Second, want: MinSessionRefreshInterval},
		{name: "medium ttl refreshes at a third", ttl: 15 * time.Minute, want: 5 * time.Minute},
		{name: "long ttl is capped at ten minutes", ttl: time.Hour, want: MaxSessionRefreshInterval},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interval := SessionRefreshIntervalFor(tc.ttl)
			if interval != tc.want {
				t.Fatalf("SessionRefreshIntervalFor(%v) = %v, want %v", tc.ttl, interval, tc.want)
			}
			if interval >= tc.ttl {
				t.Fatalf("refresh interval %v must stay below the session TTL %v", interval, tc.ttl)
			}
		})
	}
}

// TestRefreshSessionExpiryExtendsLiveSession is the unit test for the refresh
// path: renewing an almost-expired session must push its expiry past the turn
// budget instead of letting the middleware reject the token (ci#729).
func TestRefreshSessionExpiryExtendsLiveSession(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	const token = "bot-session-refresh-token"
	createSessionForTest(t, db, token, time.Now().Add(time.Minute))

	ttl := SessionTTL(30 * time.Minute)
	before := sessionExpiryForToken(t, db, token)
	if err := RefreshSessionExpiry(db, token, ttl); err != nil {
		t.Fatalf("RefreshSessionExpiry returned an error: %v", err)
	}

	refreshed := sessionExpiryForToken(t, db, token)
	if !refreshed.After(before) {
		t.Fatalf("refresh must extend the expiry: before=%v after=%v", before, refreshed)
	}
	if refreshed.Before(time.Now().Add(30 * time.Minute)) {
		t.Fatalf("refreshed expiry %v must cover the 30 minute turn budget", refreshed)
	}
}

func TestRefreshSessionExpiryReportsMissingSession(t *testing.T) {
	db := setupSessionLifetimeDB(t)

	if err := RefreshSessionExpiry(db, "no-such-token", SessionTTL(30*time.Minute)); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected gorm.ErrRecordNotFound for a deleted session, got %v", err)
	}
	if err := RefreshSessionExpiry(nil, "token", time.Minute); err == nil {
		t.Fatal("expected an error for a nil database handle")
	}
}

// TestRunSessionRefreshKeepsSessionAliveDuringTurn drives the renewal loop with a
// short interval and asserts the expiry keeps being pushed forward while the turn
// context is live, then stops cleanly.
func TestRunSessionRefreshKeepsSessionAliveDuringTurn(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	const token = "bot-session-live-token"
	createSessionForTest(t, db, token, time.Now().Add(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSessionRefresh(ctx, db, token, 2*time.Second, 20*time.Millisecond)
	}()

	time.Sleep(120 * time.Millisecond)
	live := sessionExpiryForToken(t, db, token)
	if !live.After(time.Now()) {
		t.Fatalf("session must stay valid for the whole turn, expiry=%v", live)
	}
	if live.Before(time.Now().Add(time.Second)) {
		t.Fatalf("session expiry %v was not refreshed forward during the turn", live)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunSessionRefresh did not stop after the turn context was cancelled")
	}
}

// TestStartSessionRefreshStopsAndToleratesDeletedSession ensures the background
// refresher used by the bot reply handler terminates when the turn ends and
// quietly exits once the session row is deleted.
func TestStartSessionRefreshStopsAndToleratesDeletedSession(t *testing.T) {
	db := setupSessionLifetimeDB(t)
	const token = "bot-session-start-token"
	createSessionForTest(t, db, token, time.Now().Add(time.Minute))

	stop := StartSessionRefresh(db, token, time.Second)
	if err := db.Where("token = ?", token).Delete(&Session{}).Error; err != nil {
		t.Fatalf("failed to delete session: %v", err)
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stop()
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop function did not return")
	}

	// The nil/empty guards must return an inert stop function.
	StartSessionRefresh(nil, token, time.Minute)()
	StartSessionRefresh(db, "", time.Minute)()
}
