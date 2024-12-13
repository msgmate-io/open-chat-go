package database

import "gorm.io/gorm"

// Some inspiration from: https://github.com/omept/go-chat/
type Message struct {
	Model
	SenderId   uint    `json:"-" gorm:"index"`
	Sender     User    `json:"-" gorm:"foreignKey:SenderId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ReceiverId uint    `json:"-" gorm:"index"`
	Receiver   User    `json:"-" gorm:"foreignKey:ReceiverId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	DataType   string  `json:"data_type" gorm:"default:'text'"`
	ChatId     uint    `json:"-" gorm:"index"`
	Chat       Chat    `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Content    *[]byte `json:"-"`
	Text       *string `json:"text"`
}

type Chat struct {
	Model
	User1Id         uint     `json:"-" gorm:"index"`
	User2Id         uint     `json:"-" gorm:"index"`
	User1           User     `json:"user1" gorm:"foreignKey:User1Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	User2           User     `json:"user2" gorm:"foreignKey:User2Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LatestMessageId *uint    `json:"-" gorm:"index"`
	LatestMessage   *Message `json:"latest_message" gorm:"foreignKey:LatestMessageId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}

type ChatSettings struct {
	Model
	ChatId uint `json:"ChatId" gorm:"index"`
	Chat   Chat `json:"Chat" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}

type Contact struct {
	Model
	ContactToken  string `json:"contact_token" gorm:"index"`
	OwningUserId  uint   `json:"-" gorm:"index"`
	ContactUserId uint   `json:"-" gorm:"index"`
	OwningUser    User   `json:"-" gorm:"foreignKey:OwningUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ContactUser   User   `json:"contact_user" gorm:"foreignKey:ContactUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}

type Node struct {
	Model
	NodeName  string        `json:"node_name" gorm:"index"`
	Addresses []NodeAddress `json:"addresses" gorm:"foreignKey:NodeID;references:ID"`
}

type NodeAddress struct {
	Model
	NodeID      uint   `gorm:"index" json:"-"`
	PartnetNode Node   `json:"-" gorm:"foreignKey:NodeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Address     string `json:"address"`
}

func (node *Node) List(DB *gorm.DB) []Node {
	var nodes []Node
	DB.Find(&nodes)
	return nodes
}

func (contact *Contact) List(DB *gorm.DB, owningUser User) []Contact {
	var contacts []Contact
	DB.Where("owning_user_id = ?", owningUser.ID).Find(&contacts)
	return contacts
}
