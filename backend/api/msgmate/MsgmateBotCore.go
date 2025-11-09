package msgmate

import (
	wsapi "backend/api/websocket"
	"backend/client"
	"backend/database"
	"context"
	"sync"
)

// BotConfig represents the configuration for a Msgmate bot
type BotConfig struct {
	Host     string
	Username string
	Password string
}

// BotContext represents the runtime context for a bot instance
type BotContext struct {
	Config       BotConfig
	Client       *client.Client
	BotUser      database.User
	WSHandler    *wsapi.WebSocketHandler
	ChatCanceler *ChatCanceler
	SessionMu    sync.Mutex
}

// BotInterface defines the core interface for bot operations
type BotInterface interface {
	// Start starts the bot with automatic restart capability
	Start(ctx context.Context) error

	// Stop gracefully stops the bot
	Stop() error

	// IsRunning returns true if the bot is currently running
	IsRunning() bool
}

// MessageProcessor defines the interface for processing incoming messages
type MessageProcessor interface {
	// ProcessMessage processes a single incoming message
	ProcessMessage(ctx context.Context, rawMessage []byte) error

	// PreProcessMessage extracts message metadata
	PreProcessMessage(rawMessage []byte) (messageType, chatUUID, senderUUID string, err error)
}

// WebSocketManager defines the interface for WebSocket connection management
type WebSocketManager interface {
	// Connect establishes a WebSocket connection
	Connect(ctx context.Context) error

	// Disconnect closes the WebSocket connection
	Disconnect() error

	// ReadMessages continuously reads messages from the WebSocket
	ReadMessages(ctx context.Context) error

	// IsConnected returns true if the WebSocket is connected
	IsConnected() bool
}

// SessionManager defines the interface for session management
type SessionManager interface {
	// RefreshSession refreshes the bot's session
	RefreshSession() error

	// GetSessionID returns the current session ID
	GetSessionID() string

	// StartSessionRefresh starts the background session refresh routine
	StartSessionRefresh(ctx context.Context) <-chan struct{}
}

// AIHandler defines the interface for AI response generation
type AIHandler interface {
	// GenerateResponse generates an AI response for a message
	GenerateResponse(ctx context.Context, message wsapi.NewMessage) error

	// ProcessCommand processes bot commands (like /pong, /loop)
	ProcessCommand(ctx context.Context, command string, message wsapi.NewMessage) error
}

// FileHandler defines the interface for file attachment processing
type FileHandler interface {
	// ProcessAttachments processes file attachments in a message
	ProcessAttachments(attachments []interface{}, backend string) ([]map[string]interface{}, error)

	// RetrieveFileData retrieves file data by ID
	RetrieveFileData(fileID string) (base64Data, contentType string, err error)

	// UploadToOpenAI uploads a file to OpenAI's API
	UploadToOpenAI(fileID, mimeType string) (openAIFileID string, err error)
}

// RestartManager defines the interface for bot restart management
type RestartManager interface {
	// StartWithRestart starts the bot with automatic restart capability
	StartWithRestart(ctx context.Context, bot BotInterface) error

	// LogError logs an error to disk
	LogError(err error, attempt int, username string)
}

// ChatCanceler manages cancellation contexts for different chats
type ChatCanceler struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewChatCanceler creates a new ChatCanceler instance
func NewChatCanceler() *ChatCanceler {
	return &ChatCanceler{
		cancels: make(map[string]context.CancelFunc),
	}
}

// Store stores a cancellation function for a chat
func (cc *ChatCanceler) Store(chatUUID string, cancel context.CancelFunc) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.cancels[chatUUID] = cancel
}

// Load retrieves a cancellation function for a chat
func (cc *ChatCanceler) Load(chatUUID string) (context.CancelFunc, bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cf, ok := cc.cancels[chatUUID]
	return cf, ok
}

// Delete removes a cancellation function for a chat
func (cc *ChatCanceler) Delete(chatUUID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	delete(cc.cancels, chatUUID)
}

// CancelChatResponse cancels the response for a specific chat
func CancelChatResponse(chatCanceler *ChatCanceler, chatUUID string) {
	if cancel, found := chatCanceler.Load(chatUUID); found {
		cancel()
		chatCanceler.Delete(chatUUID)
	}
}
