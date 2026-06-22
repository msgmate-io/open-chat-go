package server

import (
	"backend/api/websocket"
	"backend/database"
	"fmt"
	"net/http"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

func BackendServer(
	DB *gorm.DB,
	queueClient *asynq.Client,
	queueInspector *asynq.Inspector,
	asynqUIHandler http.Handler,
	host string,
	port int64,
	debug bool,
	frontendProxy string,
	storybookProxy string,
	sessionCookieDomain string,
) (*http.Server, *websocket.WebSocketHandler, string, error) {
	fullHost := fmt.Sprintf("http://%s:%d", host, port)
	router, websocketHandler := BackendRouting(DB, queueClient, queueInspector, asynqUIHandler, debug, frontendProxy, storybookProxy, sessionCookieDomain)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: router,
	}

	return httpServer, websocketHandler, fullHost, nil
}

func SetupBaseConnections(
	DB *gorm.DB,
	_ uint, baseBotId uint,
) error {
	var botUser database.User
	if err := DB.First(&botUser, "id = ?", baseBotId).Error; err != nil {
		return err
	}

	var users []database.User
	if err := DB.Where("id <> ? AND is_automated = ?", botUser.ID, false).Find(&users).Error; err != nil {
		return err
	}

	for _, user := range users {
		if err := ensureUserConnectedToDefaultBot(DB, user, botUser); err != nil {
			return err
		}
	}

	return nil
}

func ensureUserConnectedToDefaultBot(DB *gorm.DB, user database.User, botUser database.User) error {
	var contact database.Contact
	err := DB.Where("owning_user_id = ? AND contact_user_id = ?", user.ID, botUser.ID).First(&contact).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return err
		}

		contact = database.Contact{
			OwningUserId:  user.ID,
			ContactUserId: botUser.ID,
		}
		if err := DB.Create(&contact).Error; err != nil {
			return err
		}
	}

	// first check if already a chat between these two exists
	var chat database.Chat
	err = DB.Where(
		"(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
		user.ID,
		botUser.ID,
		botUser.ID,
		user.ID,
	).First(&chat).Error
	if err == nil {
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	if user.ID < botUser.ID {
		chat = database.Chat{
			User1Id: user.ID,
			User2Id: botUser.ID,
		}
	} else {
		chat = database.Chat{
			User1Id: botUser.ID,
			User2Id: user.ID,
		}
	}
	if err := DB.Create(&chat).Error; err != nil {
		return err
	}

	// Now create a hello word message from the bot to the user
	text := "Hello World"
	message := database.Message{
		SenderId:   botUser.ID,
		ReceiverId: user.ID,
		ChatId:     chat.ID,
		Text:       &text,
	}

	if err := DB.Create(&message).Error; err != nil {
		return err
	}

	// Now we update the chat again with the latest chat id
	chat.LatestMessageId = &message.ID
	if err := DB.Save(&chat).Error; err != nil {
		return err
	}

	return nil
}
