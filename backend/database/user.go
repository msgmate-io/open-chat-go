package database

import (
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/mail"
)

type User struct {
	Model
	Name         string `json:"name"`
	Email        string `json:"-" gorm:"unique"`
	PasswordHash string `json:"-"`
	ContactToken string `json:"contact_token"`
	IsAdmin      bool   `json:"is_admin"`
}

type Contact struct {
	Model
	ContactToken  string `json:"contact_token" gorm:"index"`
	OwningUserId  uint   `json:"-" gorm:"index"`
	ContactUserId uint   `json:"-" gorm:"index"`
	OwningUser    User   `json:"-" gorm:"foreignKey:OwningUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ContactUser   User   `json:"contact_user" gorm:"foreignKey:ContactUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}

func (u *User) AddContact(
	DB *gorm.DB,
	user *User,
) (*Contact, error) {
	contact := Contact{
		OwningUserId:  u.ID,
		ContactUserId: user.ID,
	}

	r := DB.Create(&contact)

	if r.Error != nil {
		return nil, r.Error
	}

	return &contact, nil
}

func RegisterUser(
	DB *gorm.DB,
	name string,
	email string,
	password []byte,
) (*User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)

	if err != nil {
		return nil, err
	}

	_, err = mail.ParseAddress(email)
	if err != nil {
		return nil, err
	}

	var user User = User{
		Name:         name,
		Email:        email,
		PasswordHash: string(hashedPassword),
	}

	r := DB.Create(&user)

	if r.Error != nil {
		return nil, r.Error
	}

	return &user, nil
}
