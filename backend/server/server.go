package server

import (
	"backend/api"
	"backend/api/integrations"
	"backend/api/websocket"
	"backend/database"
	"backend/scheduler"
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
	"strings"
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
	federationHandler api.FederationHandlerInterface,
	schedulerService *scheduler.SchedulerService,
	host string,
	port int64,
	debug bool,
	ssl bool,
	sslKeyPrefix string,
	frontendProxy string,
	cookieDomain string,
) (*http.Server, *websocket.WebSocketHandler, *integrations.SignalIntegrationService, string, error) {
	var protocol string
	var fullHost string
	var router *http.ServeMux
	var websocketHandler *websocket.WebSocketHandler
	var signalService *integrations.SignalIntegrationService
	var server *http.Server

	// Create the Signal Integration Service with the correct protocol
	if ssl {
		protocol = "https"

		// Load domain proxies to build SNI-based TLS configuration
		// This is non-blocking - if it fails, we continue with empty domain proxies
		domainProxies := make(map[string]tls.Certificate)
		if err := loadDomainProxies(DB, domainProxies); err != nil {
			log.Printf("Warning: Failed to load domain proxies during startup: %v", err)
			log.Printf("Domain proxies will be loaded on-demand when requests arrive")
		}

		// Load default certificate
		var certPEM, keyPEM, issuerPEM database.Key
		var certPEMBytes, keyPEMBytes []byte
		// for tls proviving keyPrefix is required!
		q := DB.Where("key_type = ? AND key_name = ?", "cert", fmt.Sprintf("%s_cert.pem", sslKeyPrefix)).First(&certPEM)
		if q.Error != nil {
			return nil, nil, nil, "", fmt.Errorf("Couldn't find cert key for node, if you want to use TLS for this proxy create the keys first!")
		}
		q = DB.Where("key_type = ? AND key_name = ?", "key", fmt.Sprintf("%s_key.pem", sslKeyPrefix)).First(&keyPEM)
		if q.Error != nil {
			return nil, nil, nil, "", fmt.Errorf("Couldn't find key key for node, if you want to use TLS for this proxy create the keys first!")
		}
		q = DB.Where("key_type = ? AND key_name = ?", "issuer", fmt.Sprintf("%s_issuer.pem", sslKeyPrefix)).First(&issuerPEM)
		if q.Error != nil {
			return nil, nil, nil, "", fmt.Errorf("Couldn't find issuer key for node, if you want to use TLS for this proxy create the keys first!")
		}

		certPEMBytes = certPEM.KeyContent
		keyPEMBytes = keyPEM.KeyContent
		defaultCert, err := tls.X509KeyPair(certPEMBytes, keyPEMBytes)
		if err != nil {
			log.Printf("Error loading certificates: %v", err)
			return nil, nil, nil, "", fmt.Errorf("Error loading certificates: %v", err)
		}

		// Create SNI-based TLS configuration
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{defaultCert},
			GetCertificate: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				// Check if we have a certificate for this domain
				if cert, exists := domainProxies[clientHello.ServerName]; exists {
					log.Printf("Using domain-specific certificate for: %s", clientHello.ServerName)
					return &cert, nil
				}

				// Try to reload domain proxies on-demand in case they weren't loaded at startup
				if len(domainProxies) == 0 {
					log.Printf("No domain proxies loaded, attempting to reload for: %s", clientHello.ServerName)
					if err := loadDomainProxies(DB, domainProxies); err != nil {
						log.Printf("Failed to reload domain proxies: %v", err)
					} else {
						// Check again after reload
						if cert, exists := domainProxies[clientHello.ServerName]; exists {
							log.Printf("Using domain-specific certificate for: %s (reloaded)", clientHello.ServerName)
							return &cert, nil
						}
					}
				}

				// Fall back to default certificate
				log.Printf("Using default certificate for: %s", clientHello.ServerName)
				return &defaultCert, nil
			},
		}

		fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

		signalService = integrations.NewSignalIntegrationService(DB, schedulerService, fmt.Sprintf("%s://%s:%d", protocol, host, port))
		router, websocketHandler = BackendRouting(DB, federationHandler, schedulerService, signalService, debug, frontendProxy, cookieDomain)
		server = &http.Server{
			Addr:      fmt.Sprintf("%s:%d", host, port),
			Handler:   router,
			TLSConfig: tlsConfig,
		}
	} else {
		protocol = "http"
		fullHost = fmt.Sprintf("%s://%s:%d", protocol, host, port)

		signalService = integrations.NewSignalIntegrationService(DB, schedulerService, fmt.Sprintf("%s://%s:%d", protocol, host, port))
		router, websocketHandler = BackendRouting(DB, federationHandler, schedulerService, signalService, debug, frontendProxy, cookieDomain)
		server = &http.Server{
			Addr:    fmt.Sprintf("%s:%d", host, port),
			Handler: router,
		}
	}

	return server, websocketHandler, signalService, fullHost, nil
}

// New function to create an HTTP fallback server for local access when TLS is enabled
func CreateHTTPFallbackServer(
	DB *gorm.DB,
	federationHandler api.FederationHandlerInterface,
	schedulerService *scheduler.SchedulerService,
	host string,
	port int64,
	debug bool,
	frontendProxy string,
	cookieDomain string,
	mainServerURL string, // Add parameter for main server URL
) (*http.Server, error) {
	// Create a separate HTTP server for local access
	// This allows local clients to connect even when TLS certificates are expired
	signalService := integrations.NewSignalIntegrationService(DB, schedulerService, mainServerURL)
	// Use the same routing as the main server to ensure all middleware and authentication works
	router, _ := BackendRouting(DB, federationHandler, schedulerService, signalService, debug, frontendProxy, cookieDomain)

	// Wrap the router with localhost-only middleware for security
	localhostOnlyRouter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only allow connections from localhost/127.0.0.1
		clientIP := r.RemoteAddr
		if strings.Contains(clientIP, "127.0.0.1") || strings.Contains(clientIP, "localhost") || strings.Contains(clientIP, "::1") {
			router.ServeHTTP(w, r)
		} else {
			log.Printf("HTTP fallback server: Rejected connection from non-localhost IP: %s", clientIP)
			http.Error(w, "Access denied - HTTP fallback server is for localhost only", http.StatusForbidden)
		}
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: localhostOnlyRouter,
	}

	return server, nil
}

// loadDomainProxies loads all active domain proxies and their certificates
// This function is designed to be non-blocking and will not fail the server startup
func loadDomainProxies(DB *gorm.DB, domainProxies map[string]tls.Certificate) error {
	// Query for active domain proxies
	var proxies []database.Proxy
	err := DB.Where("kind = ? AND active = ? AND use_tls = ?", "domain", true, true).Find(&proxies).Error
	if err != nil {
		return fmt.Errorf("failed to query domain proxies: %w", err)
	}

	log.Printf("Found %d active domain proxies", len(proxies))

	for _, proxy := range proxies {
		// TrafficOrigin contains the domain name
		domain := proxy.TrafficOrigin
		if domain == "" {
			log.Printf("Warning: Domain proxy %d has empty TrafficOrigin, skipping", proxy.ID)
			continue
		}

		// TrafficTarget contains "cert_prefix:backend_port" format
		targetParts := strings.Split(proxy.TrafficTarget, ":")
		if len(targetParts) < 1 {
			log.Printf("Warning: Domain proxy %d has invalid TrafficTarget format: %s", proxy.ID, proxy.TrafficTarget)
			continue
		}
		certPrefix := targetParts[0]

		// Load certificate for this domain
		var certPEM, keyPEM database.Key
		err := DB.Where("key_type = ? AND key_name = ?", "cert", fmt.Sprintf("%s_cert.pem", certPrefix)).First(&certPEM).Error
		if err != nil {
			log.Printf("Warning: Could not find certificate for domain %s with prefix %s: %v", domain, certPrefix, err)
			continue
		}

		err = DB.Where("key_type = ? AND key_name = ?", "key", fmt.Sprintf("%s_key.pem", certPrefix)).First(&keyPEM).Error
		if err != nil {
			log.Printf("Warning: Could not find key for domain %s with prefix %s: %v", domain, certPrefix, err)
			continue
		}

		// Create TLS certificate
		cert, err := tls.X509KeyPair(certPEM.KeyContent, keyPEM.KeyContent)
		if err != nil {
			log.Printf("Warning: Failed to create TLS certificate for domain %s: %v", domain, err)
			continue
		}

		domainProxies[domain] = cert
		log.Printf("Successfully loaded domain proxy certificate for: %s", domain)
	}

	return nil
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
