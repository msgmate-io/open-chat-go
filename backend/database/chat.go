package database

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
