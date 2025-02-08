//go:build !federation

package federation_factory

import (
	"backend/api"
	"backend/database"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"gorm.io/gorm"
	"io"
	"net/http"
	"time"
)

// FederationFactory creates the appropriate federation handler based on build tags
type FederationFactory struct{}

// NewFederationHandler creates a federation handler with federation disabled
func (f *FederationFactory) NewFederationHandler(DB *gorm.DB, host string, p2pPort int, hostPort int, useSsl bool, domain string, fallbackEnabled bool, fallbackPort int) (api.FederationHandlerInterface, error) {
	return &StubFederationHandler{}, nil
}

// NewFederationHandlerFromExisting creates a federation handler from an existing host
func (f *FederationFactory) NewFederationHandlerFromExisting(host interface{}, gater interface{}) api.FederationHandlerInterface {
	return &StubFederationHandler{}
}

// CreateHost creates a libp2p host
func (f *FederationFactory) CreateHost(DB *gorm.DB, port int, randomness io.Reader) (interface{}, interface{}, error) {
	return nil, nil, nil
}

// StartProxies starts federation proxies
func (f *FederationFactory) StartProxies(DB *gorm.DB, handler api.FederationHandlerInterface) {
	// No-op when federation is disabled
}

// PreloadPeerstore preloads the peerstore
func (f *FederationFactory) PreloadPeerstore(DB *gorm.DB, handler api.FederationHandlerInterface) error {
	return nil
}

// InitializeNetworks initializes federation networks
func (f *FederationFactory) InitializeNetworks(DB *gorm.DB, handler api.FederationHandlerInterface, host string, hostPort int, useSsl bool, domain string, fallbackEnabled bool, fallbackPort int) {
	// No-op when federation is disabled
}

// RegisterNodeRaw registers a node in the federation
func (f *FederationFactory) RegisterNodeRaw(DB *gorm.DB, handler api.FederationHandlerInterface, registerNode interface{}, timestamp *time.Time) (interface{}, error) {
	return nil, nil
}

// StubFederationHandler provides no-op implementations when federation is disabled
type StubFederationHandler struct{}

// All methods return appropriate no-op responses
func (s *StubFederationHandler) Identity(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) NetworkRequestRelayReservation(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) NetworkForwardRelayReservation(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) ListNetworks(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) NetworkCreate(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) DeleteNetwork(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) DeleteNodeFromNetwork(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) RestoreNodeFromNetwork(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) NetworkCreateRAW(DB *gorm.DB, username, password string) error {
	return nil
}

func (s *StubFederationHandler) RegisterNode(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) GetNode(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) WhitelistedPeers(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) RequestNode(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) RequestNodeByPeerId(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) CreateAndStartProxy(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) ListProxies(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) DeleteProxy(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) ReloadDomainProxies(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) UploadBinary(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) RequestSelfUpdate(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) DownloadBinary(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) GetHiveSetupScript(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) SyncGet(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) WebTerminalHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Federation not available", http.StatusNotImplemented)
}

func (s *StubFederationHandler) GetIdentity() api.Identity {
	return api.Identity{}
}

func (s *StubFederationHandler) Host() host.Host {
	return nil
}

func (s *StubFederationHandler) RemoveExpiredProxies(DB *gorm.DB) error {
	return nil
}

func (s *StubFederationHandler) StartEgressProxy(DB *gorm.DB, proxy database.Proxy, originNode, targetNode database.Node, originPort, networkName string, protocolID protocol.ID) {
	// No-op
}

func (s *StubFederationHandler) StartSSHProxy(port int, originPort string) {
	// No-op
}

func (s *StubFederationHandler) AutoRemoveExpiredProxies(DB *gorm.DB) {
	// No-op
}

func (s *StubFederationHandler) AddNetworkPeerId(networkName, peerId string) {
	// No-op
}

func (s *StubFederationHandler) GetNetworkPeerIds(networkName string) map[string]bool {
	return make(map[string]bool)
}

func (s *StubFederationHandler) StartNetworkSyncProcess(DB *gorm.DB, networkName string) {
	// No-op
}

func (s *StubFederationHandler) CreateIncomingRequestStreamHandler(scheme, host, domain string, hostPort int, allowedPaths []string, networkName string) network.StreamHandler {
	return nil
}

func (s *StubFederationHandler) Gater() *api.WhitelistGater {
	return nil
}
