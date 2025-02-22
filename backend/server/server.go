package server

import (
	"backend/api/websocket"
	"backend/database"
	"fmt"
	"net/http"

	"gorm.io/gorm"
)

func BackendServer(
	DB *gorm.DB,
	host string,
	port int64,
	debug bool,
	frontendProxy string,
	sessionCookieDomain string,
) (*http.Server, *websocket.WebSocketHandler, string, error) {
	fullHost := fmt.Sprintf("http://%s:%d", host, port)
	router, websocketHandler := BackendRouting(DB, debug, frontendProxy, sessionCookieDomain)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: router,
	}

	return httpServer, websocketHandler, fullHost, nil
}

func SetupBaseConnections(
	DB *gorm.DB,
	adminUserId uint, baseBotId uint,
) error {
	var adminUser database.User
	if err := DB.First(&adminUser, "id = ?", adminUserId).Error; err != nil {
		return err
	}

	var botUser database.User
	if err := DB.First(&botUser, "id = ?", baseBotId).Error; err != nil {
		return err
	}

	// first check if already a chat between these two exists
	var chat database.Chat
	DB.Where("user1_id = ? AND user2_id = ?", adminUser.ID, botUser.ID).First(&chat)
	if chat.ID != 0 {
		return nil
	}

	// add to each others contacts
	contact := database.Contact{
		OwningUserId:  adminUser.ID,
		ContactUserId: botUser.ID,
	}

	r := DB.Create(&contact)

	if r.Error != nil {
		return r.Error
	}

	chat = database.Chat{
		User1Id: contact.OwningUserId,
		User2Id: contact.ContactUserId,
	}

	r = DB.Create(&chat)

	if r.Error != nil {
		return r.Error
	}

	// Now create a hello word message from the bot to the user
	text := "Hello World"
	message := database.Message{
		SenderId:   botUser.ID,
		ReceiverId: adminUser.ID,
		ChatId:     chat.ID,
		Text:       &text,
	}

	r = DB.Create(&message)

	if r.Error != nil {
		return r.Error
	}

	// Now we update the chat again with the latest chat id
	chat.LatestMessageId = &message.ID
	r = DB.Save(&chat)

	if r.Error != nil {
		return r.Error
	}

	return nil
}
