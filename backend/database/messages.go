package database

import (
	"gorm.io/gorm"
)

// Some inspiration from: https://github.com/omept/go-chat/
type Message struct {
	gorm.Model
	SenderId   uint   `json:"SenderId" gorm:"index"`
	Sender     User   `json:"User" gorm:"foreignKey:SenderId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ReceiverId uint   `json:"ReceiverId" gorm:"index"`
	Receiver   User   `json:"Receiver" gorm:"foreignKey:ReceiverId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	DataType   string `json:"DataType" gorm:"default:'text'"`
	Content    []byte `json:"Content"`
}

type Chat struct {
	gorm.Model
	User1Id         uint    `json:"User1Id" gorm:"index"`
	User2Id         uint    `json:"User2Id" gorm:"index"`
	User1           User    `json:"User1" gorm:"foreignKey:User1Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	User2           User    `json:"User2" gorm:"foreignKey:User2Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LatestMessageId uint    `json:"LatestMessageId" gorm:"index"`
	LatestMessage   Message `json:"Latest" gorm:"foreignKey:LatestMessageId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	// TODO Chat Settings?
}

type ChatSettings struct {
	gorm.Model
	ChatId uint `json:"ChatId" gorm:"index"`
	Chat   Chat `json:"Chat" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	// TODO
}

type Contact struct {
	gorm.Model
	OwningUserId  uint `json:"OwningUserId" gorm:"index"`
	ContactUserId uint `json:"ContactUserId" gorm:"index"`
	OwningUser    User `json:"OwningUser" gorm:"foreignKey:OwningUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Contact       User `json:"Contact" gorm:"foreignKey:ContactUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}

func (contact *Contact) List(
	owningUser User,
) []Contact {
	var contacts []Contact
	DB.Where("owning_user_id = ?", owningUser.ID).Find(&contacts)
	return contacts
}
