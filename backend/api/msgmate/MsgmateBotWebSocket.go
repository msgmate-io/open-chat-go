package msgmate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// WebSocketManagerImpl implements the WebSocketManager interface
type WebSocketManagerImpl struct {
	botContext *BotContext
	conn       *websocket.Conn
	connected  bool
	closeConn  chan struct{}
}

// NewWebSocketManager creates a new WebSocket manager
func NewWebSocketManager(botContext *BotContext) *WebSocketManagerImpl {
	return &WebSocketManagerImpl{
		botContext: botContext,
		connected:  false,
		closeConn:  make(chan struct{}),
	}
}

// Connect establishes a WebSocket connection
func (wsm *WebSocketManagerImpl) Connect(ctx context.Context) error {
	hostNoProto := strings.Replace(strings.Replace(wsm.botContext.Config.Host, "http://", "", 1), "https://", "", 1)

	wsm.botContext.SessionMu.Lock()
	wsSessionId := wsm.botContext.Client.GetSessionId()
	wsm.botContext.SessionMu.Unlock()

	// Use wss:// for secure connections, ws:// for insecure
	protocol := "ws://"
	if strings.Contains(hostNoProto, "https://") {
		protocol = "wss://"
	}

	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("%s%s/ws/connect", protocol, hostNoProto), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{fmt.Sprintf("session_id=%s", wsSessionId)},
		},
	})

	if err != nil {
		return fmt.Errorf("WebSocket connection error: %w", err)
	}

	wsm.conn = conn
	wsm.connected = true
	log.Println("Bot connected to WebSocket")

	return nil
}

// Disconnect closes the WebSocket connection
func (wsm *WebSocketManagerImpl) Disconnect() error {
	if wsm.conn != nil {
		err := wsm.conn.Close(websocket.StatusNormalClosure, "disconnect")
		wsm.connected = false
		return err
	}
	return nil
}

// ReadMessages continuously reads messages from the WebSocket
func (wsm *WebSocketManagerImpl) ReadMessages(ctx context.Context) error {
	if !wsm.connected || wsm.conn == nil {
		return fmt.Errorf("WebSocket not connected")
	}

	// Use a channel to close the connection on session refresh
	var closeOnce sync.Once

	// Watch for session refresh
	go func() {
		select {
		case <-wsm.closeConn:
			closeOnce.Do(func() {
				log.Println("Closing WebSocket due to session refresh...")
				wsm.conn.Close(websocket.StatusNormalClosure, "session refresh")
				wsm.connected = false
			})
		case <-ctx.Done():
			closeOnce.Do(func() {
				log.Println("Closing WebSocket due to context cancellation...")
				wsm.conn.Close(websocket.StatusNormalClosure, "context cancelled")
				wsm.connected = false
			})
		}
	}()

	// Blocking call to continuously read messages
	err := wsm.readWebSocketMessages(ctx)
	if err != nil {
		log.Printf("Error reading from WebSocket: %v", err)
	}

	return err
}

// IsConnected returns true if the WebSocket is connected
func (wsm *WebSocketManagerImpl) IsConnected() bool {
	return wsm.connected
}

// SetCloseChannel sets the channel for session refresh signals
func (wsm *WebSocketManagerImpl) SetCloseChannel(closeChan chan struct{}) {
	wsm.closeConn = closeChan
}

// readWebSocketMessages reads and processes messages from the WebSocket
func (wsm *WebSocketManagerImpl) readWebSocketMessages(ctx context.Context) error {
	for {
		var rawMessage json.RawMessage
		err := wsjson.Read(ctx, wsm.conn, &rawMessage)
		if err != nil {
			// Differentiating between normal disconnection and error
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				log.Println("WebSocket closed normally")
				return nil
			}
			return fmt.Errorf("read error: %w", err) // Signal upstream to reconnect
		}

		// Process the message using the message processor
		messageProcessor := NewMessageProcessor(wsm.botContext)
		err = messageProcessor.ProcessMessage(ctx, rawMessage)
		if err != nil {
			log.Printf("Error processing message: %v", err)
			continue // Continue reading messages even if processing one fails
		}
	}
}

// WebSocketManagerFactory creates a WebSocket manager with the given context
func WebSocketManagerFactory(botContext *BotContext) WebSocketManager {
	return NewWebSocketManager(botContext)
}
