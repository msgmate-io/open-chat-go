package database

import (
	"encoding/json"
	"time"
)

// MatrixClientState stores the state of a Matrix client including encryption keys
// This is required for maintaining E2E encryption across restarts
type MatrixClientState struct {
	Model
	IntegrationID uint        `json:"integration_id" gorm:"uniqueIndex;not null"`
	Integration   Integration `json:"-" gorm:"foreignKey:IntegrationID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	// Matrix client identifiers
	UserID     string `json:"user_id" gorm:"index"`
	DeviceID   string `json:"device_id"`
	Homeserver string `json:"homeserver"`

	// Encrypted access token (stored encrypted in production)
	AccessToken string `json:"-" gorm:"type:text"`

	// Sync state
	NextBatch     string `json:"next_batch"`      // Matrix sync token for resuming sync
	FilterID      string `json:"filter_id"`       // Stored filter ID for sync
	LastSyncAt    *time.Time `json:"last_sync_at"` // Last successful sync timestamp

	// Crypto state - stores the olm/megolm session data
	// This is a binary blob that the mautrix crypto store serializes
	CryptoPickle []byte `json:"-" gorm:"type:blob"`

	// Account data pickle for olm account
	OlmAccountPickle []byte `json:"-" gorm:"type:blob"`

	// Device verification state
	DeviceVerified bool   `json:"device_verified" gorm:"default:false"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	VerificationMethod string `json:"verification_method,omitempty"`

	// Trust state
	CrossSigningSetup bool `json:"cross_signing_setup" gorm:"default:false"`

	// Metadata
	DisplayName string `json:"display_name,omitempty"`
}

// MatrixRoom stores Matrix room information for the integration
type MatrixRoom struct {
	Model
	MatrixClientStateID uint              `json:"matrix_client_state_id" gorm:"index;not null"`
	MatrixClientState   MatrixClientState `json:"-" gorm:"foreignKey:MatrixClientStateID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	// Room identifiers
	RoomID    string `json:"room_id" gorm:"uniqueIndex:idx_room_client"`
	RoomAlias string `json:"room_alias,omitempty"`

	// Room info
	Name       string `json:"name,omitempty"`
	Topic      string `json:"topic,omitempty"`
	AvatarURL  string `json:"avatar_url,omitempty"`
	IsEncrypted bool   `json:"is_encrypted" gorm:"default:false"`
	IsDirect    bool   `json:"is_direct" gorm:"default:false"`

	// Associated chat in our system
	ChatID *uint `json:"chat_id,omitempty" gorm:"index"`
	Chat   *Chat `json:"-" gorm:"foreignKey:ChatID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`

	// Sync state
	LastEventID    string     `json:"last_event_id,omitempty"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`

	// Whitelist status for AI processing
	WhitelistEnabled bool `json:"whitelist_enabled" gorm:"default:false"`
}

// MatrixDevice stores other devices for the Matrix user (for verification)
type MatrixDevice struct {
	Model
	MatrixClientStateID uint              `json:"matrix_client_state_id" gorm:"index;not null"`
	MatrixClientState   MatrixClientState `json:"-" gorm:"foreignKey:MatrixClientStateID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	// Device identifiers
	UserID   string `json:"user_id" gorm:"index"`
	DeviceID string `json:"device_id" gorm:"uniqueIndex:idx_device_user"`

	// Device info
	DisplayName string `json:"display_name,omitempty"`

	// Key information
	IdentityKey string `json:"identity_key,omitempty"`
	SigningKey  string `json:"signing_key,omitempty"`

	// Trust state
	Verified    bool       `json:"verified" gorm:"default:false"`
	Blacklisted bool       `json:"blacklisted" gorm:"default:false"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`

	// When was this device last seen
	LastSeenIP string     `json:"last_seen_ip,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// MatrixOutboundSession stores outbound megolm sessions for sending encrypted messages
type MatrixOutboundSession struct {
	Model
	MatrixClientStateID uint              `json:"matrix_client_state_id" gorm:"index;not null"`
	MatrixClientState   MatrixClientState `json:"-" gorm:"foreignKey:MatrixClientStateID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	// Session identifiers
	RoomID    string `json:"room_id" gorm:"index"`
	SessionID string `json:"session_id" gorm:"uniqueIndex"`

	// Session data (pickled)
	SessionPickle []byte `json:"-" gorm:"type:blob"`

	// Message index
	MessageIndex uint32 `json:"message_index"`

	// Shared with devices (JSON array of device IDs)
	SharedWith json.RawMessage `json:"shared_with" gorm:"type:jsonb"`

	// Expiry
	CreatedAt time.Time `json:"created_at"`
	MaxAge    int64     `json:"max_age"` // in milliseconds
}

// MatrixInboundSession stores inbound megolm sessions for receiving encrypted messages
type MatrixInboundSession struct {
	Model
	MatrixClientStateID uint              `json:"matrix_client_state_id" gorm:"index;not null"`
	MatrixClientState   MatrixClientState `json:"-" gorm:"foreignKey:MatrixClientStateID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	// Session identifiers
	RoomID    string `json:"room_id" gorm:"index"`
	SenderKey string `json:"sender_key"`
	SessionID string `json:"session_id" gorm:"uniqueIndex"`

	// Session data (pickled)
	SessionPickle []byte `json:"-" gorm:"type:blob"`

	// Forwarding chain (JSON array)
	ForwardingChain json.RawMessage `json:"forwarding_chain" gorm:"type:jsonb"`
}

// MatrixConfig represents the configuration for a Matrix integration
type MatrixConfig struct {
	Homeserver  string `json:"homeserver"`
	UserID      string `json:"user_id"`
	DeviceID    string `json:"device_id"`
	AccessToken string `json:"access_token"`
	DisplayName string `json:"display_name,omitempty"`

	// Optional settings
	EnableEncryption bool `json:"enable_encryption,omitempty"`
	AutoJoinRooms    bool `json:"auto_join_rooms,omitempty"`
	ProcessInvites   bool `json:"process_invites,omitempty"`
}

// Validate validates the Matrix configuration
func (c *MatrixConfig) Validate() error {
	if c.Homeserver == "" {
		return ErrMissingHomeserver
	}
	if c.UserID == "" {
		return ErrMissingUserID
	}
	if c.DeviceID == "" {
		return ErrMissingDeviceID
	}
	if c.AccessToken == "" {
		return ErrMissingAccessToken
	}
	return nil
}

// Custom errors for Matrix config validation
type MatrixConfigError string

func (e MatrixConfigError) Error() string {
	return string(e)
}

const (
	ErrMissingHomeserver  MatrixConfigError = "homeserver is required"
	ErrMissingUserID      MatrixConfigError = "user_id is required"
	ErrMissingDeviceID    MatrixConfigError = "device_id is required"
	ErrMissingAccessToken MatrixConfigError = "access_token is required"
)
