package util

import (
	"backend/api/websocket"
	"backend/database"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"log"
	"net/http"
)

func GetDBAndUser(r *http.Request) (*gorm.DB, *database.User, error) {
	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		return nil, nil, errors.New("invalid database")
	}

	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		return nil, nil, errors.New("invalid user")
	}
	return DB, user, nil
}

func GetDB(r *http.Request) (*gorm.DB, error) {
	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		return nil, errors.New("invalid database")
	}
	return DB, nil
}

func GetWebsocket(r *http.Request) (*websocket.WebSocketHandler, error) {
	websocket, ok := r.Context().Value("websocket").(*websocket.WebSocketHandler)
	if !ok {
		return nil, errors.New("invalid websocket")
	}
	return websocket, nil
}

func CreateUser(
	DB *gorm.DB,
	username string,
	password string,
	isAdminUser bool,
) (error, *database.User) {
	log.Println("Creating root user")
	// first chaeck if that user already exists
	var user database.User
	q := DB.First(&user, "email = ?", username)

	if q.Error != nil {
		if q.Error.Error() != "record not found" {
			log.Fatal(q.Error)
			return fmt.Errorf("Error reading user from db"), nil
		}
	} else {
		log.Println("User already exists")
		return nil, &user
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

	if err != nil {
		log.Fatal(err)
	}

	user = database.User{
		Name:         username,
		Email:        username,
		PasswordHash: string(hashedPassword),
		ContactToken: uuid.New().String(),
		IsAdmin:      isAdminUser,
	}

	q = DB.Create(&user)

	if q.Error != nil {
		log.Fatal(q.Error)
		return fmt.Errorf("Error writing user to db"), nil
	}

	return nil, &user
}

func CreateRootUser(DB *gorm.DB, username string, password string) (error, *database.User) {
	// First check if one IsAdmin user already exists
	var user database.User
	q := DB.First(&user, "is_admin = ?", true)

	if q.Error != nil {
		return CreateUser(DB, username, password, true)
	}

	return nil, &user
}
