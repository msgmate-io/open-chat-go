//go:build !federation

package api

import (
	"gorm.io/gorm"
	"io"
	"time"
)

// FederationFactory creates the appropriate federation handler based on build tags
type FederationFactory struct{}

// NewFederationHandler creates a federation handler with federation disabled
func (f *FederationFactory) NewFederationHandler(DB *gorm.DB, host string, p2pPort int, hostPort int, useSsl bool, domain string, fallbackEnabled bool, fallbackPort int) (FederationHandlerInterface, error) {
	// Return stub implementation when federation is disabled
	return &StubFederationHandler{}, nil
}

// NewFederationHandlerFromExisting creates a federation handler from an existing host
func (f *FederationFactory) NewFederationHandlerFromExisting(host interface{}, gater interface{}) FederationHandlerInterface {
	// Return stub implementation when federation is disabled
	return &StubFederationHandler{}
}

// CreateHost creates a libp2p host
func (f *FederationFactory) CreateHost(DB *gorm.DB, port int, randomness io.Reader) (interface{}, interface{}, error) {
	// Return nil when federation is disabled
	return nil, nil, nil
}

// StartProxies starts federation proxies
func (f *FederationFactory) StartProxies(DB *gorm.DB, handler FederationHandlerInterface) {
	// No-op when federation is disabled
}

// PreloadPeerstore preloads the peerstore
func (f *FederationFactory) PreloadPeerstore(DB *gorm.DB, handler FederationHandlerInterface) error {
	// No-op when federation is disabled
	return nil
}

// InitializeNetworks initializes federation networks
func (f *FederationFactory) InitializeNetworks(DB *gorm.DB, handler FederationHandlerInterface, host string, hostPort int, useSsl bool, domain string, fallbackEnabled bool, fallbackPort int) {
	// No-op when federation is disabled
}

// RegisterNodeRaw registers a node in the federation
func (f *FederationFactory) RegisterNodeRaw(DB *gorm.DB, handler FederationHandlerInterface, registerNode interface{}, timestamp *time.Time) (interface{}, error) {
	// No-op when federation is disabled
	return nil, nil
}
