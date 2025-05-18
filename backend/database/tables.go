package database

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type Migration interface {
	Migrate(*gorm.DB) error
}

type TableMigration struct {
	Model interface{}
}

func (t TableMigration) Migrate(db *gorm.DB) error {
	return db.AutoMigrate(t.Model)
}

type FunctionMigration struct {
	Name string
	Run  func(*gorm.DB) error
}

func (m FunctionMigration) Migrate(db *gorm.DB) error {
	if m.Run == nil {
		return nil
	}
	return m.Run(db)
}

type ChatAndMessageMigration struct{}

type TempChat struct {
	Model
	User1Id         uint   `gorm:"index"`
	User2Id         uint   `gorm:"index"`
	User1           User   `gorm:"foreignKey:User1Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	User2           User   `gorm:"foreignKey:User2Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LatestMessageId *uint  `gorm:"index"`
	SharedConfigId  *uint  `gorm:"index"`
	ChatType        string `gorm:"default:'conversation'"`
}

func (TempChat) TableName() string {
	return "chats"
}

type TempSharedConfig struct {
	Model
	ChatId     uint            `gorm:"index"`
	ConfigData json.RawMessage `gorm:"type:jsonb"`
}

func (TempSharedConfig) TableName() string {
	return "shared_chat_configs"
}

type TempMessage struct {
	Model
	ReadAt     *time.Time `gorm:"default:null"`
	SenderId   uint       `gorm:"index"`
	ReceiverId uint       `gorm:"index"`
	DataType   string     `gorm:"default:'text'"`
	ChatId     uint       `gorm:"index"`
	Content    *[]byte
	Text       *string
}

func (TempMessage) TableName() string {
	return "messages"
}

func (c ChatAndMessageMigration) Migrate(db *gorm.DB) error {
	if !db.Migrator().HasTable("chats") {
		if err := db.Set("gorm:table_options", "").Migrator().CreateTable(&TempChat{}); err != nil {
			return fmt.Errorf("failed to create chat table: %v", err)
		}
		fmt.Println("Chat table created")
	} else {
		// Check if ChatType column exists, add it if not
		if !db.Migrator().HasColumn(&Chat{}, "chat_type") {
			if err := db.Migrator().AddColumn(&Chat{}, "chat_type"); err != nil {
				return fmt.Errorf("failed to add chat_type column: %v", err)
			}
			// Set default value for existing rows
			if err := db.Exec("UPDATE chats SET chat_type = 'conversation' WHERE chat_type IS NULL").Error; err != nil {
				return fmt.Errorf("failed to set default chat_type for existing rows: %v", err)
			}
			fmt.Println("Added chat_type column to chats table")
		}
	}

	if !db.Migrator().HasTable("shared_chat_configs") {
		if err := db.Set("gorm:table_options", "").Migrator().CreateTable(&TempSharedConfig{}); err != nil {
			return fmt.Errorf("failed to create shared chat config table: %v", err)
		}
		fmt.Println("SharedChatConfig table created")
	}

	if !db.Migrator().HasTable("messages") {
		if err := db.Set("gorm:table_options", "").Migrator().CreateTable(&TempMessage{}); err != nil {
			return fmt.Errorf("failed to create message table: %v", err)
		}
		fmt.Println("Message table created")
	}

	if err := db.AutoMigrate(&Message{}); err != nil {
		return fmt.Errorf("failed to add constraints to message: %v", err)
	}

	if err := db.AutoMigrate(&SharedChatConfig{}); err != nil {
		return fmt.Errorf("failed to add constraints to shared chat config: %v", err)
	}

	if err := db.AutoMigrate(&Chat{}); err != nil {
		return fmt.Errorf("failed to add constraints to chat: %v", err)
	}

	return nil
}

type FileUploadMigration struct{}

func (FileUploadMigration) Migrate(db *gorm.DB) error {
	db.SetupJoinTable(&UploadedFile{}, "SharedWith", &FileAccess{})
	return db.AutoMigrate(&UploadedFile{}, &FileAccess{})
}

type GrantDefaultPermissionsMigration struct{}

func (GrantDefaultPermissionsMigration) Migrate(db *gorm.DB) error {
	var users []User
	if err := db.Find(&users).Error; err != nil {
		return err
	}
	for _, user := range users {
		permission := Permission{UserId: user.ID, Permission: PermissionCreateAPITokens}
		if err := db.Where("user_id = ? AND permission = ?", user.ID, PermissionCreateAPITokens).FirstOrCreate(&permission).Error; err != nil {
			return err
		}
		createBotsPermission := Permission{UserId: user.ID, Permission: PermissionCreateBots}
		if err := db.Where("user_id = ? AND permission = ?", user.ID, PermissionCreateBots).FirstOrCreate(&createBotsPermission).Error; err != nil {
			return err
		}
		if err := EnsureDefaultAccessTokenForUser(db, user.ID); err != nil {
			return err
		}
	}
	return nil
}

type BackfillUsernamesMigration struct{}

func (BackfillUsernamesMigration) Migrate(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable("users") || !db.Migrator().HasColumn(&User{}, "username") {
		return nil
	}
	users := []User{}
	if err := db.Where("username IS NULL OR username = ''").Find(&users).Error; err != nil {
		return err
	}
	for _, user := range users {
		candidate := strings.TrimSpace(user.Email)
		if candidate == "" {
			generated, err := EnsureUniqueRandomUsername(db)
			if err != nil {
				return err
			}
			candidate = generated
		}
		if err := db.Model(&User{}).Where("id = ?", user.ID).Update("username", candidate).Error; err != nil {
			return err
		}
	}
	return nil
}

type BackfillBotRuntimeOwnersMigration struct{}

func (BackfillBotRuntimeOwnersMigration) Migrate(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable("bot_runtime_configs") || !db.Migrator().HasTable("bot_runtime_owners") {
		return nil
	}

	type runtimeOwnerRow struct {
		ID          uint
		OwnerUserId uint
	}
	rows := []runtimeOwnerRow{}
	if err := db.Model(&BotRuntimeConfig{}).Select("id", "owner_user_id").Find(&rows).Error; err != nil {
		return err
	}

	for _, row := range rows {
		if row.ID == 0 || row.OwnerUserId == 0 {
			continue
		}
		owner := BotRuntimeOwner{BotRuntimeConfigId: row.ID, UserId: row.OwnerUserId}
		if err := db.Where("bot_runtime_config_id = ? AND user_id = ?", row.ID, row.OwnerUserId).FirstOrCreate(&owner).Error; err != nil {
			return err
		}
	}

	return nil
}

var Tabels []interface{} = []interface{}{
	&User{},
	&TwoFactorRecoveryCode{},
	&Session{},
	&PublicProfile{},
	&Contact{},
	&Chat{},
	&SharedChatConfig{},
	&ChatSettings{},
	&SharedChatInstance{},
	&Message{},
	&UploadedFile{},
	&FileAccess{},
	&TaskResult{},
	&ModelConfig{},
	&BotRuntimeConfig{},
	&BotRuntimeOwner{},
	&Permission{},
	&AccessToken{},
	&IntegrationAccess{},
}

var Migrations []Migration = []Migration{
	TableMigration{&User{}},
	BackfillUsernamesMigration{},
	TableMigration{&TwoFactorRecoveryCode{}},
	TableMigration{&Session{}},
	TableMigration{&PublicProfile{}},
	TableMigration{&Contact{}},
	ChatAndMessageMigration{}, // Migrates: 'Chat', 'SharedChatConfig', 'Message'
	TableMigration{&ChatSettings{}},
	TableMigration{&SharedChatInstance{}},
	FileUploadMigration{},
	TableMigration{&ToolInitData{}},
	TableMigration{&TaskResult{}},
	TableMigration{&ModelConfig{}},
	TableMigration{&BotRuntimeConfig{}},
	TableMigration{&BotRuntimeOwner{}},
	BackfillBotRuntimeOwnersMigration{},
	TableMigration{&Permission{}},
	TableMigration{&AccessToken{}},
	TableMigration{&IntegrationAccess{}},
	GrantDefaultPermissionsMigration{},
}

func RegisterExternalModels(models ...interface{}) {
	for _, model := range models {
		if model == nil {
			continue
		}
		Tabels = append(Tabels, model)
		Migrations = append(Migrations, TableMigration{Model: model})
	}
}

func RegisterExternalMigrations(migrations ...FunctionMigration) {
	for _, migration := range migrations {
		if migration.Run == nil {
			continue
		}
		Migrations = append(Migrations, migration)
	}
}
