package server

import (
	"backend/api/federation"
	"backend/api/websocket"
	"backend/database"
	"bufio"
	"crypto/tls"
	"fmt"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
	"io"
	"log"
	"net/http"
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
	federationHandler *federation.FederationHandler,
	host string,
	port int64,
	debug bool,
	ssl bool,
	sslKeyPrefix string,
	frontendProxy string,
	cookieDomain string,
) (*http.Server, *websocket.WebSocketHandler, string, error) {
	var protocol string
	var fullHost string
	var router *http.ServeMux
	var websocketHandler *websocket.WebSocketHandler

	var server *http.Server
	if ssl {
		protocol = "https"

		var certPEM, keyPEM, issuerPEM database.Key
		var certPEMBytes, keyPEMBytes []byte
		// for tls proviving keyPrefix is required!
		q := DB.Where("key_type = ? AND key_name = ?", "cert", fmt.Sprintf("%s_cert.pem", sslKeyPrefix)).First(&certPEM)
		if q.Error != nil {
			return nil, nil, "", fmt.Errorf("Couldn't find cert key for node, if you want to use TLS for this proxy create the keys first!")
		}
		q = DB.Where("key_type = ? AND key_name = ?", "key", fmt.Sprintf("%s_key.pem", sslKeyPrefix)).First(&keyPEM)
		if q.Error != nil {
			return nil, nil, "", fmt.Errorf("Couldn't find key key for node, if you want to use TLS for this proxy create the keys first!")
		}
		q = DB.Where("key_type = ? AND key_name = ?", "issuer", fmt.Sprintf("%s_issuer.pem", sslKeyPrefix)).First(&issuerPEM)
		if q.Error != nil {
			return nil, nil, "", fmt.Errorf("Couldn't find issuer key for node, if you want to use TLS for this proxy create the keys first!")
		}

		certPEMBytes = certPEM.KeyContent
		keyPEMBytes = keyPEM.KeyContent
		cert, err := tls.X509KeyPair(certPEMBytes, keyPEMBytes)
		if err != nil {
			log.Printf("Error loading certificates: %v", err)
			return nil, nil, "", fmt.Errorf("Error loading certificates: %v", err)
		}
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
		}

		fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

		router, websocketHandler = BackendRouting(DB, federationHandler, debug, frontendProxy, cookieDomain)
		server = &http.Server{
			Addr:      fmt.Sprintf("%s:%d", host, port),
			Handler:   router,
			TLSConfig: tlsConfig,
		}
	} else {
		protocol = "http"
		fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

		router, websocketHandler = BackendRouting(DB, federationHandler, debug, frontendProxy, cookieDomain)
		server = &http.Server{
			Addr:    fmt.Sprintf("%s:%d", host, port),
			Handler: router,
		}
	}

	return server, websocketHandler, fullHost, nil
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
