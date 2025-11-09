package msgmate

import (
	"context"
	"fmt"
	"log"
	"time"
)

// SessionManagerImpl implements the SessionManager interface
type SessionManagerImpl struct {
	botContext      *BotContext
	refreshInterval time.Duration
	sessionRefresh  chan struct{}
}

// NewSessionManager creates a new session manager
func NewSessionManager(botContext *BotContext) *SessionManagerImpl {
	return &SessionManagerImpl{
		botContext:      botContext,
		refreshInterval: 12 * time.Hour, // Refresh session every 12 hours
		sessionRefresh:  make(chan struct{}, 1),
	}
}

// RefreshSession refreshes the bot's session
func (sm *SessionManagerImpl) RefreshSession() error {
	sm.botContext.SessionMu.Lock()
	defer sm.botContext.SessionMu.Unlock()

	err, _ := sm.botContext.Client.LoginUser(sm.botContext.Config.Username, sm.botContext.Config.Password)
	if err != nil {
		return fmt.Errorf("failed to refresh session: %w", err)
	}

	log.Println("Session refreshed successfully")
	return nil
}

// GetSessionID returns the current session ID
func (sm *SessionManagerImpl) GetSessionID() string {
	sm.botContext.SessionMu.Lock()
	defer sm.botContext.SessionMu.Unlock()
	return sm.botContext.Client.GetSessionId()
}

// StartSessionRefresh starts the background session refresh routine
func (sm *SessionManagerImpl) StartSessionRefresh(ctx context.Context) <-chan struct{} {
	go func() {
		ticker := time.NewTicker(sm.refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("Session refresh routine cancelled")
				return
			case <-ticker.C:
				log.Println("Refreshing bot session...")
				if err := sm.RefreshSession(); err != nil {
					log.Printf("Failed to refresh session: %v", err)
					continue
				}

				// Signal main loop to reconnect WebSocket
				select {
				case sm.sessionRefresh <- struct{}{}:
					log.Println("Session refresh signal sent")
				default:
					// If signal already pending, skip
				}
			}
		}
	}()

	return sm.sessionRefresh
}

// GetRefreshChannel returns the session refresh channel
func (sm *SessionManagerImpl) GetRefreshChannel() <-chan struct{} {
	return sm.sessionRefresh
}

// SessionManagerFactory creates a session manager with the given context
func SessionManagerFactory(botContext *BotContext) SessionManager {
	return NewSessionManager(botContext)
}
