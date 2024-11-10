package database

import (
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/mail"
)

type User struct {
	gorm.Model
	Name         string
	Email        string `gorm:"unique"`
	PasswordHash string
}

func RegisterUser(
	db *gorm.DB,
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

	r := db.Create(&user)

	if r.Error != nil {
		return nil, r.Error
	}

	return &user, r.Error
}
