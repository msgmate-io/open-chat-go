package msgmate

import (
	wsapi "backend/api/websocket"
	"backend/database"
	"context"
	"errors"
	"sync"

	client "github.com/msgmate-io/go-client-integration/goclient"
)

var ErrResponseAlreadySent = errors.New("ai response already sent")

// BotConfig represents the configuration for a Msgmate bot
type BotConfig struct {
	Host     string
	Username string
	Password string
}

// BotContext represents the runtime context for a bot instance
type BotContext struct {
	Config    BotConfig
	Client    *client.Client
	BotUser   database.User
	WSHandler *wsapi.WebSocketHandler
	SessionMu sync.Mutex
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
