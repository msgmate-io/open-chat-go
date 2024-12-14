package util

import (
	"backend/api/websocket"
	"backend/database"
	"errors"
	"gorm.io/gorm"
	"net/http"
)

func GetDBAndUser(r *http.Request) (*gorm.DB, *database.User, error) {
	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		return nil, nil, errors.New("invalid database")
	}

	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		return nil, nil, errors.New("invalid user")
	}
	return DB, user, nil
}

func GetWebsocket(r *http.Request) (*websocket.WebSocketHandler, error) {
	websocket, ok := r.Context().Value("websocket").(*websocket.WebSocketHandler)
	if !ok {
		return nil, errors.New("invalid websocket")
	}
	return websocket, nil
}
