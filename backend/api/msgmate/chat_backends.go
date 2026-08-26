package msgmate

import (
	wsapi "backend/api/websocket"
	"backend/database"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// ChatBackendRequest carries everything an external chat backend needs to
// generate and persist a bot reply for a chat.
type ChatBackendRequest struct {
	DB         *gorm.DB
	BotContext *BotContext
	Message    wsapi.NewMessage
	Config     map[string]interface{}
}

// ChatBackendFunc generates a bot reply for a chat using an external backend
// (instead of the built-in LLM path). Implementations are expected to persist
// the reply via BotContext.Client so it lands in the chat history and fans out
// over websockets.
type ChatBackendFunc func(ctx context.Context, req ChatBackendRequest) error

var (
	chatBackendMu       sync.RWMutex
	chatBackendRegistry = map[string]ChatBackendFunc{}
)

// RegisterChatBackend registers an external chat backend under a name that maps
// to the chat shared-config "backend" key.
func RegisterChatBackend(name string, fn ChatBackendFunc) {
	chatBackendMu.Lock()
	defer chatBackendMu.Unlock()
	chatBackendRegistry[strings.ToLower(strings.TrimSpace(name))] = fn
}

func lookupChatBackend(name string) (ChatBackendFunc, bool) {
	chatBackendMu.RLock()
	defer chatBackendMu.RUnlock()
	fn, ok := chatBackendRegistry[strings.ToLower(strings.TrimSpace(name))]
	return fn, ok
}

// ResolveChatBackend loads a chat's shared config and returns the registered
// external chat backend named by the "backend" key, if any. The parsed config map
// is returned alongside so the backend can read its own keys.
func ResolveChatBackend(db *gorm.DB, chatUUID string) (ChatBackendFunc, map[string]interface{}, bool) {
	if db == nil {
		return nil, nil, false
	}
	var chat database.Chat
	if err := db.Preload("SharedConfig").Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		return nil, nil, false
	}
	if chat.SharedConfig == nil || len(chat.SharedConfig.ConfigData) == 0 {
		return nil, nil, false
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err != nil {
		return nil, nil, false
	}
	backendName := ""
	if v, ok := config["backend"].(string); ok {
		backendName = strings.ToLower(strings.TrimSpace(v))
	}
	if backendName == "" {
		return nil, nil, false
	}
	fn, ok := lookupChatBackend(backendName)
	if !ok {
		return nil, nil, false
	}
	return fn, config, true
}
