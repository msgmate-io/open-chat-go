package integrations

import (
	"backend/database"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// MatrixIntegrationService manages Matrix client connections and encryption
type MatrixIntegrationService struct {
	DB            *gorm.DB
	serverURL     string
	activeClients map[uint]*MatrixClientConnection // keyed by integration ID
	mu            sync.RWMutex
	stopChannels  map[uint]chan struct{}
	stopMu        sync.Mutex
	botService    *MatrixBotService
}

// MatrixClientConnection represents an active Matrix client connection
type MatrixClientConnection struct {
	Integration   database.Integration
	Client        *mautrix.Client
	ClientState   *database.MatrixClientState
	Done          chan struct{}
	LastSyncToken string
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewMatrixIntegrationService creates a new Matrix integration service
func NewMatrixIntegrationService(DB *gorm.DB, serverURL string) *MatrixIntegrationService {
	botService := NewMatrixBotService(DB, serverURL)
	return &MatrixIntegrationService{
		DB:            DB,
		serverURL:     serverURL,
		activeClients: make(map[uint]*MatrixClientConnection),
		stopChannels:  make(map[uint]chan struct{}),
		botService:    botService,
	}
}

// StartAllActiveIntegrations starts all active Matrix integrations
func (s *MatrixIntegrationService) StartAllActiveIntegrations() {
	var integrations []database.Integration
	result := s.DB.Where("integration_type = ? AND active = ?", "matrix", true).Find(&integrations)
	if result.Error != nil {
		log.Printf("[Matrix:Service] Error finding active Matrix integrations: %v", result.Error)
		return
	}

	log.Printf("[Matrix:Service] Found %d active Matrix integrations", len(integrations))
	for _, integration := range integrations {
		if err := s.StartIntegrationWithRestart(integration); err != nil {
			log.Printf("[Matrix:Service] Error starting Matrix integration %s: %v", integration.IntegrationName, err)
		}
	}
}

// StartIntegrationWithRestart starts an integration with automatic restart on failure
func (s *MatrixIntegrationService) StartIntegrationWithRestart(integration database.Integration) error {
	s.stopMu.Lock()
	if _, exists := s.stopChannels[integration.ID]; exists {
		s.stopMu.Unlock()
		return fmt.Errorf("integration %d already has an active restart loop", integration.ID)
	}

	stopCh := make(chan struct{})
	s.stopChannels[integration.ID] = stopCh
	s.stopMu.Unlock()

	go func() {
		restartCount := 0
		maxRestartDelay := 60 * time.Second
		baseRestartDelay := 5 * time.Second
		maxRestartAttempts := 1000

		for restartCount < maxRestartAttempts {
			select {
			case <-stopCh:
				log.Printf("[Matrix:%d] Integration restart loop stopped", integration.ID)
				return
			default:
			}

			restartCount++
			log.Printf("[Matrix:%d] Starting integration (attempt %d)", integration.ID, restartCount)

			err := s.StartIntegration(integration)
			if err != nil {
				log.Printf("[Matrix:%d] Integration crashed (attempt %d): %v", integration.ID, restartCount, err)
			} else {
				log.Printf("[Matrix:%d] Integration stopped normally", integration.ID)
			}

			// Calculate restart delay with exponential backoff
			restartDelay := time.Duration(restartCount) * baseRestartDelay
			if restartDelay > maxRestartDelay {
				restartDelay = maxRestartDelay
			}

			select {
			case <-stopCh:
				return
			case <-time.After(restartDelay):
			}
		}

		log.Printf("[Matrix:%d] Max restart attempts reached", integration.ID)
	}()

	return nil
}

// StartIntegration starts a Matrix client for a specific integration
func (s *MatrixIntegrationService) StartIntegration(integration database.Integration) error {
	// Parse the integration config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	homeserver, _ := config["homeserver"].(string)
	userIDStr, _ := config["user_id"].(string)

	// Get the Matrix client state from the database
	var clientState database.MatrixClientState
	if err := s.DB.Where("integration_id = ?", integration.ID).First(&clientState).Error; err != nil {
		return fmt.Errorf("failed to get Matrix client state: %w", err)
	}

	// Check if we already have an active connection
	s.mu.Lock()
	if _, exists := s.activeClients[integration.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("client for integration %d already exists", integration.ID)
	}
	s.mu.Unlock()

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Create the Matrix client
	client, err := mautrix.NewClient(homeserver, id.UserID(userIDStr), clientState.AccessToken)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create Matrix client: %w", err)
	}

	// Set the device ID
	client.DeviceID = id.DeviceID(clientState.DeviceID)

	// Create connection object
	conn := &MatrixClientConnection{
		Integration:   integration,
		Client:        client,
		ClientState:   &clientState,
		Done:          make(chan struct{}),
		LastSyncToken: clientState.NextBatch,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Register event handlers
	syncer := client.Syncer.(*mautrix.DefaultSyncer)

	// Handle encrypted messages (will need libolm for decryption)
	syncer.OnEventType(event.EventEncrypted, func(ctx context.Context, evt *event.Event) {
		s.handleEncryptedMessage(conn, evt)
	})

	// Handle regular messages
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		s.handleMessage(conn, evt)
	})

	// Handle room invites
	syncer.OnEventType(event.StateMember, func(ctx context.Context, evt *event.Event) {
		s.handleMemberEvent(conn, evt)
	})

	// Handle verification requests
	syncer.OnEventType(event.ToDeviceVerificationRequest, func(ctx context.Context, evt *event.Event) {
		s.handleVerificationRequest(conn, evt)
	})

	// Add the client to active clients
	s.mu.Lock()
	s.activeClients[integration.ID] = conn
	s.mu.Unlock()

	log.Printf("[Matrix:%d] Starting sync for user %s", integration.ID, userIDStr)

	// Set the sync token if we have one
	if conn.LastSyncToken != "" {
		client.Store.SaveNextBatch(ctx, id.UserID(userIDStr), conn.LastSyncToken)
	}

	// Start syncing (this blocks until context is cancelled or error occurs)
	err = client.SyncWithContext(ctx)

	// Cleanup
	s.mu.Lock()
	delete(s.activeClients, integration.ID)
	s.mu.Unlock()

	// Save the sync token
	nextBatch, _ := client.Store.LoadNextBatch(ctx, id.UserID(userIDStr))
	if nextBatch != "" {
		clientState.NextBatch = nextBatch
		now := time.Now()
		clientState.LastSyncAt = &now
		s.DB.Save(&clientState)
	}

	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("sync error: %w", err)
	}

	return nil
}

// StopIntegration stops a Matrix client connection
func (s *MatrixIntegrationService) StopIntegration(integrationID uint) error {
	// Stop the restart loop
	s.stopMu.Lock()
	if stopCh, exists := s.stopChannels[integrationID]; exists {
		close(stopCh)
		delete(s.stopChannels, integrationID)
	}
	s.stopMu.Unlock()

	// Stop the client
	s.mu.Lock()
	conn, exists := s.activeClients[integrationID]
	s.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active client for integration %d", integrationID)
	}

	// Cancel the context to stop syncing
	conn.cancel()
	close(conn.Done)

	log.Printf("[Matrix:%d] Integration stopped", integrationID)
	return nil
}

// StopAllIntegrations stops all active Matrix integrations
func (s *MatrixIntegrationService) StopAllIntegrations() {
	// Stop all restart loops
	s.stopMu.Lock()
	for id, stopCh := range s.stopChannels {
		close(stopCh)
		log.Printf("[Matrix:%d] Stopped restart loop", id)
	}
	s.stopChannels = make(map[uint]chan struct{})
	s.stopMu.Unlock()

	// Stop all active clients
	s.mu.Lock()
	for id := range s.activeClients {
		s.mu.Unlock()
		s.StopIntegration(id)
		s.mu.Lock()
	}
	s.mu.Unlock()
}

// handleEncryptedMessage handles encrypted messages
// Note: Full E2E encryption requires libolm to be installed on the system
func (s *MatrixIntegrationService) handleEncryptedMessage(conn *MatrixClientConnection, evt *event.Event) {
	log.Printf("[Matrix:%d] Received encrypted message from %s in room %s (E2E decryption requires libolm)",
		conn.Integration.ID, evt.Sender, evt.RoomID)

	// For now, we log that we received an encrypted message
	// Full decryption support requires libolm to be installed and crypto helper to be enabled
	// The message will be stored with a note that it's encrypted

	if s.botService != nil {
		// Create a placeholder content indicating encrypted message
		content := &event.MessageEventContent{
			MsgType: event.MsgNotice,
			Body:    "[Encrypted message - E2E decryption not available]",
		}
		if err := s.botService.ProcessMessage(conn, evt, content); err != nil {
			log.Printf("[Matrix:%d] Error processing encrypted message: %v", conn.Integration.ID, err)
		}
	}
}

// handleMessage handles unencrypted messages
func (s *MatrixIntegrationService) handleMessage(conn *MatrixClientConnection, evt *event.Event) {
	s.processMessage(conn, evt)
}

// processMessage processes a Matrix message event
func (s *MatrixIntegrationService) processMessage(conn *MatrixClientConnection, evt *event.Event) {
	// Skip messages from ourselves
	if evt.Sender == conn.Client.UserID {
		return
	}

	// Get the message content
	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok {
		log.Printf("[Matrix:%d] Failed to parse message content", conn.Integration.ID)
		return
	}

	log.Printf("[Matrix:%d] Received message from %s in room %s: %s",
		conn.Integration.ID, evt.Sender, evt.RoomID, content.Body)

	// Process with bot service
	if s.botService != nil {
		if err := s.botService.ProcessMessage(conn, evt, content); err != nil {
			log.Printf("[Matrix:%d] Error processing message: %v", conn.Integration.ID, err)
		}
	}
}

// handleMemberEvent handles room member events (invites, joins, etc.)
func (s *MatrixIntegrationService) handleMemberEvent(conn *MatrixClientConnection, evt *event.Event) {
	content, ok := evt.Content.Parsed.(*event.MemberEventContent)
	if !ok {
		return
	}

	// Check if this is an invite for us
	if content.Membership == event.MembershipInvite && evt.StateKey != nil && id.UserID(*evt.StateKey) == conn.Client.UserID {
		log.Printf("[Matrix:%d] Received invite to room %s from %s",
			conn.Integration.ID, evt.RoomID, evt.Sender)

		// Check if auto-join is enabled
		var config map[string]interface{}
		if err := json.Unmarshal(conn.Integration.Config, &config); err == nil {
			if autoJoin, ok := config["auto_join_rooms"].(bool); ok && autoJoin {
				_, err := conn.Client.JoinRoom(conn.ctx, string(evt.RoomID), "", nil)
				if err != nil {
					log.Printf("[Matrix:%d] Failed to auto-join room %s: %v",
						conn.Integration.ID, evt.RoomID, err)
				} else {
					log.Printf("[Matrix:%d] Auto-joined room %s", conn.Integration.ID, evt.RoomID)
				}
			}
		}
	}
}

// handleVerificationRequest handles device verification requests
func (s *MatrixIntegrationService) handleVerificationRequest(conn *MatrixClientConnection, evt *event.Event) {
	log.Printf("[Matrix:%d] Received verification request from %s",
		conn.Integration.ID, evt.Sender)
	// Verification handling will be implemented in the verification API endpoints
}

// SendMessage sends a message to a Matrix room
func (s *MatrixIntegrationService) SendMessage(integrationID uint, roomID string, message string) error {
	s.mu.RLock()
	conn, exists := s.activeClients[integrationID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no active client for integration %d", integrationID)
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    message,
	}

	// Send message (unencrypted for now - encryption requires libolm)
	_, err := conn.Client.SendMessageEvent(conn.ctx, id.RoomID(roomID), event.EventMessage, content)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

// GetClient returns the active Matrix client for an integration
func (s *MatrixIntegrationService) GetClient(integrationID uint) (*MatrixClientConnection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, exists := s.activeClients[integrationID]
	if !exists {
		return nil, fmt.Errorf("no active client for integration %d", integrationID)
	}

	return conn, nil
}

// IsClientActive checks if a Matrix client is active for an integration
func (s *MatrixIntegrationService) IsClientActive(integrationID uint) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.activeClients[integrationID]
	return exists
}

// GetClientState gets the Matrix client state from the database
func (s *MatrixIntegrationService) GetClientState(integrationID uint) (*database.MatrixClientState, error) {
	var state database.MatrixClientState
	if err := s.DB.Where("integration_id = ?", integrationID).First(&state).Error; err != nil {
		return nil, fmt.Errorf("failed to get client state: %w", err)
	}
	return &state, nil
}

// UpdateClientState updates the Matrix client state in the database
func (s *MatrixIntegrationService) UpdateClientState(state *database.MatrixClientState) error {
	return s.DB.Save(state).Error
}
