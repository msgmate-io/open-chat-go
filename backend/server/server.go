package server

import (
	"backend/api/websocket"
	"backend/database"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	sessionCookieDomain string,
) (*http.Server, *websocket.WebSocketHandler, string, error) {
	fullHost := fmt.Sprintf("http://%s:%d", host, port)
	router, websocketHandler := BackendRouting(DB, queueClient, queueInspector, asynqUIHandler, debug, frontendProxy, sessionCookieDomain)
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

	if err := ensureAdminOwnsDefaultBotRuntime(DB, adminUser, botUser); err != nil {
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

func runtimeNameAvailableForOwner(DB *gorm.DB, ownerUserID uint, name string, excludeBotUserID uint) (bool, error) {
	var count int64
	q := DB.Model(&database.BotRuntimeConfig{}).
		Where("owner_user_id = ? AND name = ?", ownerUserID, name)
	if excludeBotUserID != 0 {
		q = q.Where("bot_user_id <> ?", excludeBotUserID)
	}
	if err := q.Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

func uniqueRuntimeNameForOwner(DB *gorm.DB, ownerUserID uint, baseName string, excludeBotUserID uint) (string, error) {
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		baseName = "default_bot"
	}

	available, err := runtimeNameAvailableForOwner(DB, ownerUserID, baseName, excludeBotUserID)
	if err != nil {
		return "", err
	}
	if available {
		return baseName, nil
	}

	for i := 2; i <= 1000; i++ {
		candidate := fmt.Sprintf("%s_%d", baseName, i)
		isAvailable, availErr := runtimeNameAvailableForOwner(DB, ownerUserID, candidate, excludeBotUserID)
		if availErr != nil {
			return "", availErr
		}
		if isAvailable {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("failed to resolve unique default bot runtime name for owner %d", ownerUserID)
}

func ensureAdminOwnsDefaultBotRuntime(DB *gorm.DB, adminUser database.User, botUser database.User) error {
	emptyConfig := json.RawMessage("{}")

	return DB.Transaction(func(tx *gorm.DB) error {
		var runtime database.BotRuntimeConfig
		err := tx.Where("bot_user_id = ?", botUser.ID).First(&runtime).Error
		if err != nil {
			if err != gorm.ErrRecordNotFound {
				return err
			}

			name, nameErr := uniqueRuntimeNameForOwner(tx, adminUser.ID, botUser.Name, botUser.ID)
			if nameErr != nil {
				return nameErr
			}

			runtime = database.BotRuntimeConfig{
				BotUserId:           botUser.ID,
				OwnerUserId:         adminUser.ID,
				Name:                name,
				Description:         "Default platform bot",
				DefaultSharedConfig: emptyConfig,
				IsPublic:            true,
				IsActive:            true,
			}
			return tx.Create(&runtime).Error
		}

		updates := map[string]interface{}{}
		if runtime.OwnerUserId != adminUser.ID {
			updates["owner_user_id"] = adminUser.ID
		}
		if strings.TrimSpace(runtime.Name) == "" {
			name, nameErr := uniqueRuntimeNameForOwner(tx, adminUser.ID, botUser.Name, botUser.ID)
			if nameErr != nil {
				return nameErr
			}
			updates["name"] = name
		}
		if len(runtime.DefaultSharedConfig) == 0 {
			updates["default_shared_config"] = emptyConfig
		}
		if !runtime.IsActive {
			updates["is_active"] = true
		}
		if !runtime.IsPublic {
			updates["is_public"] = true
		}

		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&runtime).Updates(updates).Error
	})
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
