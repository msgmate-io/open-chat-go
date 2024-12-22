package database

import (
	"gorm.io/gorm"
	"time"
)

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

type Key struct {
	Model
	KeyType    string `json:"key_type" gorm:"index"`
	KeyName    string `json:"key_name" gorm:"index"`
	KeyContent []byte `json:"key_content"`
}

type Node struct {
	Model
	NodeName     string        `json:"node_name" gorm:"index"`
	PeerID       string        `json:"peer_id"`
	LatestPingId *uint         `json:"-" gorm:"index"`
	LatestPing   *Ping         `json:"latest_ping" gorm:"foreignKey:LatestPingId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Addresses    []NodeAddress `json:"addresses" gorm:"foreignKey:NodeID;references:ID"`
}

type NodeAddress struct {
	Model
	NodeID      uint   `gorm:"index" json:"-"`
	PartnetNode Node   `json:"-" gorm:"foreignKey:NodeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Address     string `json:"address"`
}

type Proxy struct {
	Model
	NodeID uint   `json:"-"`
	Node   Node   `json:"-" gorm:"foreignKey:NodeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Port   string `json:"port"`
	Active bool   `json:"active"`
	UseTLS bool   `json:"use_tls"`
	// supported Kinds: tcp, http
	Kind string `json:"kind"`
	// supported Directions: 'egress' ( route traffic from a proxy to libp2p stream ), 'ingress' ( route traffic coming from a libp2p stream )
	Direction     string `json:"direction"`
	TrafficOrigin string `json:"traffic_origin"`
	TrafficTarget string `json:"traffic_target"`
	// If we have UseTLS=true, we assume there are 3 keys with names <proxy_id>_cert.pem, <proxy_id>_key.pem, <proxy_id>_issuer.pem
}

type Ping struct {
	Model
	NodeID   uint      `json:"-"`
	Node     Node      `json:"-" gorm:"foreignKey:NodeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	PingedAt time.Time `json:"pinged_at"`
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
