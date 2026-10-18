package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"backend/workqueue"

	"gorm.io/gorm"
)

// Automated (bot) sessions used by a bot reply turn used to be created with a
// hard-coded 15-minute expiry while the turn itself may run for the whole turn
// budget (30 minutes by default, see workqueue.BotReplyTimeout). A turn that ran
// past 15 minutes therefore had every authenticated call rejected by the auth
// middleware with 403 "Invalid token" and died mid-work (ci#729). The lifetime is
// now derived from the turn budget *and* renewed while the turn is running, so a
// session can only expire once nothing uses it any more.
const (
	// BotSessionTTLGrace is added on top of the turn budget so a turn that uses
	// its whole budget still has a valid session while it writes its final
	// result and sends the closing message.
	BotSessionTTLGrace = 5 * time.Minute

	// MinBotSessionTTL keeps the derived lifetime sane even for a tiny
	// (test/override) turn budget.
	MinBotSessionTTL = 20 * time.Minute

	// MaxSessionRefreshInterval is the ceiling for the renewal interval: a
	// generous budget is refreshed every 10 minutes at the latest.
	MaxSessionRefreshInterval = 10 * time.Minute

	// MinSessionRefreshInterval avoids a busy renewal loop for tiny budgets;
	// production always uses the ceiling above.
	MinSessionRefreshInterval = time.Minute
)

// SessionTTL resolves the lifetime of the session created for a turn from the
// turn budget instead of a fixed 15 minutes (ci#729). A non-positive budget
// falls back to the configured bot reply turn timeout.
func SessionTTL(turnBudget time.Duration) time.Duration {
	ttl := turnBudget
	if ttl <= 0 {
		ttl = workqueue.BotReplyTimeout()
	}
	ttl += BotSessionTTLGrace
	if ttl < MinBotSessionTTL {
		ttl = MinBotSessionTTL
	}
	return ttl
}

// SessionRefreshIntervalFor picks the renewal interval for a session TTL: a third
// of the lifetime, clamped so a long session is renewed at least every 10 minutes
// and a short one never spins.
func SessionRefreshIntervalFor(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return MaxSessionRefreshInterval
	}
	interval := ttl / 3
	if interval > MaxSessionRefreshInterval {
		interval = MaxSessionRefreshInterval
	}
	if interval < MinSessionRefreshInterval {
		interval = MinSessionRefreshInterval
	}
	return interval
}

// RefreshSessionExpiry pushes a live session's expiry to now+ttl. It reports
// gorm.ErrRecordNotFound when the session row is gone, which is how a finished
// turn ends the renewal loop.
func RefreshSessionExpiry(db *gorm.DB, token string, ttl time.Duration) error {
	if db == nil || token == "" || ttl <= 0 {
		return fmt.Errorf("db, token and ttl are required to refresh a session")
	}

	expiry := time.Now().Add(ttl)
	result := db.Model(&Session{}).Where("token = ?", token).Update("expiry", expiry)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Zero rows means either the session vanished (turn finished) or the
		// expiry already had exactly this value; distinguish the two.
		var count int64
		if err := db.Model(&Session{}).Where("token = ?", token).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return gorm.ErrRecordNotFound
		}
	}
	return nil
}

// RunSessionRefresh renews the session expiry every interval until ctx is done or
// the session row disappears. The token is a credential and is never logged.
func RunSessionRefresh(ctx context.Context, db *gorm.DB, token string, ttl, interval time.Duration) {
	if db == nil || token == "" || ttl <= 0 {
		return
	}
	if interval <= 0 {
		interval = SessionRefreshIntervalFor(ttl)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := RefreshSessionExpiry(db, token, ttl)
			if err == nil {
				continue
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// The session is gone: the turn already cleaned up.
				return
			}
			log.Printf("bot session refresh failed: %v", err)
		}
	}
}

// StartSessionRefresh keeps a turn's session alive in the background and returns
// the stop function the turn must defer (it waits for the goroutine to finish, so
// no refresh can race the session deletion at the end of the turn).
func StartSessionRefresh(db *gorm.DB, token string, ttl time.Duration) func() {
	if db == nil || token == "" || ttl <= 0 {
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSessionRefresh(ctx, db, token, ttl, SessionRefreshIntervalFor(ttl))
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}
