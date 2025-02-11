package database

import (
	"encoding/json"
	"fmt"
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

type ChatAndMessageMigration struct{}

type TempChat struct {
	Model
	User1Id         uint  `gorm:"index"`
	User2Id         uint  `gorm:"index"`
	User1           User  `gorm:"foreignKey:User1Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	User2           User  `gorm:"foreignKey:User2Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LatestMessageId *uint `gorm:"index"`
	SharedConfigId  *uint `gorm:"index"`
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
	}

	if !db.Migrator().HasTable("shared_chat_configs") {
		if err := db.Set("gorm:table_options", "").Migrator().CreateTable(&TempSharedConfig{}); err != nil {
			return fmt.Errorf("failed to create shared chat config table: %v", err)
		}
		fmt.Println("SharedChatConfig table created")
	}
	fmt.Println("SharedChatConfig table created")

	if !db.Migrator().HasTable("messages") {
		if err := db.Set("gorm:table_options", "").Migrator().CreateTable(&TempMessage{}); err != nil {
			return fmt.Errorf("failed to create message table: %v", err)
		}
		fmt.Println("Message table created")
	}
	fmt.Println("Message table created")

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

var Tabels []interface{} = []interface{}{
	&User{},
	&Proxy{},
	&Key{},
	&Session{},
	&PublicProfile{},
	&Contact{},
	&Network{},
	&NodeAddress{},
	&Node{},
	&NetworkMember{},
	&Chat{},
	&SharedChatConfig{},
	&ChatSettings{},
	&Message{},
	&ContactRequest{},
}

var Migrations []Migration = []Migration{
	TableMigration{&User{}},
	TableMigration{&Proxy{}},
	TableMigration{&Key{}},
	TableMigration{&Session{}},
	TableMigration{&PublicProfile{}},
	TableMigration{&Contact{}},
	TableMigration{&Network{}},
	TableMigration{&NetworkMember{}},
	TableMigration{&Node{}},
	TableMigration{&NodeAddress{}},
	ChatAndMessageMigration{}, // Migrates: 'Chat', 'SharedChatConfig', 'Message'
	TableMigration{&ChatSettings{}},
	TableMigration{&ContactRequest{}},
}
