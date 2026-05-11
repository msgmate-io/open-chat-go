package server

import (
	"backend/api/integrations"
	"backend/api/websocket"
	"backend/database"
	"backend/scheduler"
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

var Config *cli.Command

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
	DB *gorm.DB,
	schedulerService *scheduler.SchedulerService,
	host string,
	port int64,
	debug bool,
	frontendProxy string,
	cookieDomain string,
) (*http.Server, *websocket.WebSocketHandler, *integrations.SignalIntegrationService, *integrations.MatrixIntegrationService, string, error) {
	fullHost := fmt.Sprintf("http://%s:%d", host, port)
	signalService := integrations.NewSignalIntegrationService(DB, fullHost)
	matrixService := integrations.NewMatrixIntegrationService(DB, fullHost)
	router, websocketHandler := BackendRouting(DB, schedulerService, signalService, matrixService, debug, frontendProxy, cookieDomain)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: router,
	}

	return httpServer, websocketHandler, signalService, matrixService, fullHost, nil
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

	// first check if already a chat between these two exists
	var chat database.Chat
	DB.Where("user1_id = ? AND user2_id = ?", adminUser.ID, botUser.ID).First(&chat)
	if chat.ID != 0 {
		return nil
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

	chat = database.Chat{
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

	// Now we update the chat again with the latest chat id
	chat.LatestMessageId = &message.ID
	r = DB.Save(&chat)

	if r.Error != nil {
		return r.Error
	}

	return nil
}
