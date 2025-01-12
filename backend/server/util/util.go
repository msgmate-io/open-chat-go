package util

import (
	"backend/api/websocket"
	"backend/database"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"io"
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

func CreateUserPwPreHashed(
	DB *gorm.DB,
	username string,
	hashedPassword string,
	isAdminUser bool,
) (error, *database.User) {

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

	user = database.User{
		Name:         username,
		Email:        username,
		PasswordHash: hashedPassword,
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

func CreateUser(
	DB *gorm.DB,
	username string,
	password string,
	isAdminUser bool,
) (error, *database.User) {
	log.Println("Creating user", username, "isAdminUser: ", isAdminUser)
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

func Hash(data string) string {
	hash := sha256.New()
	hash.Write([]byte(data))
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func padKey(key []byte) []byte {
	switch len(key) {
	case 16, 24, 32:
		return key
	default:
		hash := sha256.Sum256(key) // Use SHA-256 to ensure a fixed length
		return hash[:32]           // Truncate to 32 bytes for AES-256
	}
}

func Encrypt(data []byte, key []byte) ([]byte, error) {
	key = padKey(key) // Ensure key is valid length

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	ciphertext := make([]byte, aes.BlockSize+len(data))
	iv := ciphertext[:aes.BlockSize]

	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], data)

	return ciphertext, nil
}

func Decrypt(ciphertext []byte, key []byte) ([]byte, error) {
	key = padKey(key) // Ensure key is valid length

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) < aes.BlockSize {
		return nil, errors.New("ciphertext too short")
	}

	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertext, ciphertext)

	return ciphertext, nil
}
