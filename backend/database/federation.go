package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// StringSlice is a custom type for handling string slices in GORM
type StringSlice []string

func (s StringSlice) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(s)
}

func (s *StringSlice) Scan(value interface{}) error {
	if value == nil {
		*s = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, s)
	case string:
		return json.Unmarshal([]byte(v), s)
	}
	return fmt.Errorf("cannot scan %T into StringSlice", value)
}

// KeyTypes: cert, key, issuer, login
type Key struct {
	Model
	Sealed     bool   `json:"sealed"`
	KeyType    string `json:"key_type" gorm:"index"`
	KeyName    string `json:"key_name" gorm:"index"`
	KeyContent []byte `json:"key_content"`
}

type Node struct {
	Model
	LastChanged time.Time     `json:"last_changed"`
	NodeName    string        `json:"node_name" gorm:"index"`
	PeerID      string        `json:"peer_id"`
	Addresses   []NodeAddress `json:"addresses" gorm:"foreignKey:NodeID;references:ID"`
}

// networks can be created and joined by anybody but the password must be provided on join!
type Network struct {
	Model
	NetworkName     string `json:"network_name" gorm:"index"`
	NetworkType     string `json:"network_type"`
	NetworkPassword string `json:"network_password"`
}

type NetworkMember struct {
	Model
	NetworkID uint       `json:"-"`
	Network   Network    `json:"-" gorm:"foreignKey:NetworkID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	NodeID    uint       `json:"-"`
	Node      Node       `json:"-" gorm:"foreignKey:NodeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LastSync  time.Time  `json:"last_sync"`
	Status    string     `json:"status"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`
}

type ContactRequest struct {
	Model
	NodeName  string   `json:"node_name"`
	Addresses []string `json:"addresses" gorm:"type:text[]"`
	Status    string   `json:"status"`
}

type NodeAddress struct {
	Model
	NodeID      uint   `gorm:"index" json:"-"`
	PartnetNode Node   `json:"-" gorm:"foreignKey:NodeID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Address     string `json:"address"`
}

// supported Kinds: tcp, http, ssh
// supported Directions: 'egress' ( route traffic from a proxy to libp2p stream ), 'ingress' ( route traffic coming from a libp2p stream )
// If we have UseTLS=true, we assume there are 3 keys with names <proxy_id>_cert.pem, <proxy_id>_key.pem, <proxy_id>_issuer.pem
type Proxy struct {
	Model
	Port          string      `json:"port"`
	Active        bool        `json:"active"`
	UseTLS        bool        `json:"use_tls"`
	Kind          string      `json:"kind"`
	Direction     string      `json:"direction"`
	NetworkName   string      `json:"network_name"`
	TrafficOrigin string      `json:"traffic_origin"`
	TrafficTarget string      `json:"traffic_target"`
	Tags          StringSlice `json:"tags" gorm:"type:json"`
	ExpiresAt     *time.Time  `json:"expires_at"`
}
