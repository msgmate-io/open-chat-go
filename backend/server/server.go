package server

import (
	"backend/api/federation"
	"backend/api/websocket"
	"backend/database"
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"io"
	"log"
	"net/http"
)

var Config *cli.Command
var ServerStatus string = "unknown"

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
	return CreateUser(DB, username, password, true)
}

func GenerateToken(email string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(email), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Hash to store:", string(hash))

	hasher := md5.New()
	hasher.Write(hash)
	return hex.EncodeToString(hasher.Sum(nil))
}

func makeHost(port int, randomness io.Reader) (host.Host, error) {
	// Creates a new RSA key pair for this host.
	prvKey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, randomness)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// TODO: allow resstriction listen address via param
	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port))
	fmt.Println("Host Listen Address:", sourceMultiAddr)

	return libp2p.New(
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(prvKey),
		// Enable stuff for hole punching etc...
	)
}

func readData(rw *bufio.ReadWriter) {
	for {
		str, err := rw.ReadString('\n')
		if err != nil {
			log.Printf("Error reading from stream: %v\n", err)
			return
		}

		if str == "" {
			return
		}
		if str != "\n" {
			fmt.Printf("Received message: %s", str)
		}
	}
}

func writeData(rw *bufio.ReadWriter, message string) error {
	if _, err := rw.WriteString(fmt.Sprintf("%s\n", message)); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	return nil
}

func BackendServer(
	db *gorm.DB,
	federationHandler *federation.FederationHandler,
	host string,
	port int64,
	debug bool,
	ssl bool,
	frontendProxy string,
) (*http.Server, *websocket.WebSocketHandler, string) {
	var protocol string
	var fullHost string

	router, websocketHandler := BackendRouting(db, federationHandler, debug, frontendProxy)
	if ssl {
		protocol = "https"
	} else {
		protocol = "http"
	}

	fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: router,
	}

	return server, websocketHandler, fullHost
}

func SetupBaseConnections(
	DB *gorm.DB,
	adminUserId uint, baseBotId uint,
) error {
	var adminUser database.User
	if err := DB.First(&adminUser, "id = ?", adminUserId).Error; err != nil {
		return err
	}

	var botUser database.User
	if err := DB.First(&botUser, "id = ?", baseBotId).Error; err != nil {
		return err
	}

	// add to each others contacts
	contact := database.Contact{
		OwningUserId:  adminUser.ID,
		ContactUserId: botUser.ID,
	}

	r := DB.Create(&contact)

	if r.Error != nil {
		return r.Error
	}

	chat := database.Chat{
		User1Id: contact.OwningUserId,
		User2Id: contact.ContactUserId,
	}

	r = DB.Create(&chat)

	if r.Error != nil {
		return r.Error
	}

	// Now create a hello word message from the bot to the user
	text := "Hello World"
	message := database.Message{
		SenderId:   botUser.ID,
		ReceiverId: adminUser.ID,
		ChatId:     chat.ID,
		Text:       &text,
	}

	r = DB.Create(&message)

	if r.Error != nil {
		return r.Error
	}

	// ok lets create another chat
	chat = database.Chat{
		User1Id: contact.OwningUserId,
		User2Id: contact.ContactUserId,
	}

	r = DB.Create(&chat)

	if r.Error != nil {
		return r.Error
	}

	return nil
}
