package tasks

import (
	wsapi "backend/api/websocket"

	"gorm.io/gorm"
)

// Deps holds shared dependencies for task handlers.
type Deps struct {
	DB          *gorm.DB
	BackendHost string
	WSHandler   *wsapi.WebSocketHandler
}
