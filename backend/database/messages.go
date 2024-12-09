package database

// Some inspiration from: https://github.com/omept/go-chat/
type Message struct {
	Model
	SenderId   uint   `json:"SenderId" gorm:"index"`
	Sender     User   `json:"User" gorm:"foreignKey:SenderId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ReceiverId uint   `json:"ReceiverId" gorm:"index"`
	Receiver   User   `json:"Receiver" gorm:"foreignKey:ReceiverId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	DataType   string `json:"DataType" gorm:"default:'text'"`
	Content    []byte `json:"Content"`
}

type Chat struct {
	Model
	User1Id         uint     `json:"-" gorm:"index"`
	User2Id         uint     `json:"-" gorm:"index"`
	User1           User     `json:"user1" gorm:"foreignKey:User1Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	User2           User     `json:"user2" gorm:"foreignKey:User2Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LatestMessageId *uint    `json:"-" gorm:"index"` // Make this a pointer
	LatestMessage   *Message `json:"latest_message" gorm:"foreignKey:LatestMessageId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	// TODO Chat Settings?
}

type ChatSettings struct {
	Model
	ChatId uint `json:"ChatId" gorm:"index"`
	Chat   Chat `json:"Chat" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	// TODO
}

type Contact struct {
	Model
	ContactToken  string `json:"contact_token" gorm:"index"`
	OwningUserId  uint   `json:"-" gorm:"index"`
	ContactUserId uint   `json:"-" gorm:"index"`
	OwningUser    User   `json:"-" gorm:"foreignKey:OwningUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ContactUser   User   `json:"contact_user" gorm:"foreignKey:ContactUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}

func (contact *Contact) List(
	owningUser User,
) []Contact {
	var contacts []Contact
	DB.Where("owning_user_id = ?", owningUser.ID).Find(&contacts)
	return contacts
}
