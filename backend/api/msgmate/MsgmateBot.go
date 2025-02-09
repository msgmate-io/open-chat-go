package msgmate

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	wsapi "backend/api/websocket"
	"backend/client"
)

// MsgmateBot implements the BotInterface
type MsgmateBot struct {
	config         BotConfig
	botContext     *BotContext
	wsManager      WebSocketManager
	sessionManager SessionManager
	running        bool
	mu             sync.RWMutex
}

// NewMsgmateBot creates a new MsgmateBot instance
func NewMsgmateBot(host, username, password string, wsHandler *wsapi.WebSocketHandler) (*MsgmateBot, error) {
	config := BotConfig{
		Host:     host,
		Username: username,
		Password: password,
	}

	// Create client and login
	ocClient := client.NewClient(host)
	err, _ := ocClient.LoginUser(username, password)
	if err != nil {
		return nil, fmt.Errorf("failed to login bot: %w", err)
	}

	err, botUser := ocClient.GetUserInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}

	// Create bot context
	botContext := &BotContext{
		Config:       config,
		Client:       ocClient,
		BotUser:      *botUser,
		WSHandler:    wsHandler,
		ChatCanceler: NewChatCanceler(),
		SessionMu:    sync.Mutex{},
	}

	// Create managers
	wsManager := WebSocketManagerFactory(botContext)
	sessionManager := SessionManagerFactory(botContext)

	return &MsgmateBot{
		config:         config,
		botContext:     botContext,
		wsManager:      wsManager,
		sessionManager: sessionManager,
		running:        false,
	}, nil
}

// Start starts the bot with automatic restart capability
func (mb *MsgmateBot) Start(ctx context.Context) error {
	mb.mu.Lock()
	mb.running = true
	mb.mu.Unlock()

	defer func() {
		mb.mu.Lock()
		mb.running = false
		mb.mu.Unlock()
	}()

	// Start session refresh routine
	sessionRefreshChan := mb.sessionManager.StartSessionRefresh(ctx)

	// Main bot loop
	for {
		select {
		case <-ctx.Done():
			log.Printf("Bot context cancelled: %v", ctx.Err())
			return ctx.Err()
		default:
			// Continue with bot logic
		}

		// Connect to WebSocket
		err := mb.wsManager.Connect(ctx)
		if err != nil {
			log.Printf("WebSocket connection error: %v", err)
			time.Sleep(5 * time.Second) // Wait before retrying to connect
			continue
		}

		// Set up session refresh handling
		if wsManagerImpl, ok := mb.wsManager.(*WebSocketManagerImpl); ok {
			// Convert receive-only channel to bidirectional channel
			closeChan := make(chan struct{})
			go func() {
				<-sessionRefreshChan
				close(closeChan)
			}()
			wsManagerImpl.SetCloseChannel(closeChan)
		}

		// Read messages from WebSocket
		err = mb.wsManager.ReadMessages(ctx)
		if err != nil {
			log.Printf("Error reading from WebSocket: %v", err)
		}

		// Check if we should reconnect due to session refresh
		select {
		case <-sessionRefreshChan:
			log.Println("WebSocket closed for session refresh, reconnecting...")
			continue
		default:
			// Normal error, reconnect after delay
			time.Sleep(5 * time.Second)
		}
	}
}

// Stop gracefully stops the bot
func (mb *MsgmateBot) Stop() error {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	if !mb.running {
		return nil
	}

	mb.running = false
	return mb.wsManager.Disconnect()
}

// IsRunning returns true if the bot is currently running
func (mb *MsgmateBot) IsRunning() bool {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	return mb.running
}

// GetBotContext returns the bot context
func (mb *MsgmateBot) GetBotContext() *BotContext {
	return mb.botContext
}
