//go:build federation

package federation_factory

import (
	"backend/api"
	"backend/api/federation"
	"backend/database"
	"backend/server"
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

// NewFederationHandler creates a federation handler with federation enabled
func (f *FederationFactory) NewFederationHandler(DB *gorm.DB, host string, p2pPort int, hostPort int, useSsl bool, domain string, fallbackEnabled bool, fallbackPort int) (api.FederationHandlerInterface, error) {
	hostPtr, federationHandler, err := server.CreateFederationHost(DB, host, p2pPort, hostPort, useSsl, domain, fallbackEnabled, fallbackPort)
	if err != nil {
		return nil, err
	}

	// Wrap the real federation handler to implement our interface
	return &FederationHandlerWrapper{
		handler: federationHandler,
		host:    *hostPtr,
	}, nil
}

// NewFederationHandlerFromExisting creates a federation handler from an existing host
func (f *FederationFactory) NewFederationHandlerFromExisting(host interface{}, gater interface{}) api.FederationHandlerInterface {
	// This would need to be implemented based on the actual federation package
	return &api.StubFederationHandler{}
}

// CreateHost creates a libp2p host
func (f *FederationFactory) CreateHost(DB *gorm.DB, port int, randomness io.Reader) (interface{}, interface{}, error) {
	return server.CreateHost(DB, port, randomness)
}

// StartProxies starts federation proxies
func (f *FederationFactory) StartProxies(DB *gorm.DB, handler api.FederationHandlerInterface) {
	if wrapper, ok := handler.(*FederationHandlerWrapper); ok {
		server.StartProxies(DB, wrapper.handler)
	}
}

// PreloadPeerstore preloads the peerstore
func (f *FederationFactory) PreloadPeerstore(DB *gorm.DB, handler api.FederationHandlerInterface) error {
	if wrapper, ok := handler.(*FederationHandlerWrapper); ok {
		return server.PreloadPeerstore(DB, wrapper.handler)
	}
	return nil
}

// InitializeNetworks initializes federation networks
func (f *FederationFactory) InitializeNetworks(DB *gorm.DB, handler api.FederationHandlerInterface, host string, hostPort int, useSsl bool, domain string, fallbackEnabled bool, fallbackPort int) {
	if wrapper, ok := handler.(*FederationHandlerWrapper); ok {
		server.InitializeNetworks(DB, wrapper.handler, host, hostPort, useSsl, domain, fallbackEnabled, fallbackPort)
	}
}

// RegisterNodeRaw registers a node in the federation
func (f *FederationFactory) RegisterNodeRaw(DB *gorm.DB, handler api.FederationHandlerInterface, registerNode interface{}, timestamp *time.Time) (interface{}, error) {
	if wrapper, ok := handler.(*FederationHandlerWrapper); ok {
		if regNode, ok := registerNode.(federation.RegisterNode); ok {
			return federation.RegisterNodeRaw(DB, wrapper.handler, regNode, timestamp)
		}
	}
	return nil, nil
}

// FederationHandlerWrapper wraps the real federation handler to implement our interface
type FederationHandlerWrapper struct {
	handler *federation.FederationHandler
	host    interface{}
}

// Implement all the interface methods by delegating to the real handler
func (w *FederationHandlerWrapper) Identity(resp http.ResponseWriter, req *http.Request) {
	w.handler.Identity(resp, req)
}

func (w *FederationHandlerWrapper) Bootstrap(resp http.ResponseWriter, req *http.Request) {
	w.handler.Bootstrap(resp, req)
}

func (w *FederationHandlerWrapper) NetworkRequestRelayReservation(resp http.ResponseWriter, req *http.Request) {
	w.handler.NetworkRequestRelayReservation(resp, req)
}

func (w *FederationHandlerWrapper) NetworkForwardRelayReservation(resp http.ResponseWriter, req *http.Request) {
	w.handler.NetworkForwardRelayReservation(resp, req)
}

func (w *FederationHandlerWrapper) ListNetworks(resp http.ResponseWriter, req *http.Request) {
	w.handler.ListNetworks(resp, req)
}

func (w *FederationHandlerWrapper) NetworkCreate(resp http.ResponseWriter, req *http.Request) {
	w.handler.NetworkCreate(resp, req)
}

func (w *FederationHandlerWrapper) DeleteNetwork(resp http.ResponseWriter, req *http.Request) {
	w.handler.DeleteNetwork(resp, req)
}

func (w *FederationHandlerWrapper) DeleteNodeFromNetwork(resp http.ResponseWriter, req *http.Request) {
	w.handler.DeleteNodeFromNetwork(resp, req)
}

func (w *FederationHandlerWrapper) RestoreNodeFromNetwork(resp http.ResponseWriter, req *http.Request) {
	w.handler.RestoreNodeFromNetwork(resp, req)
}

func (w *FederationHandlerWrapper) NetworkCreateRAW(DB *gorm.DB, username, password string) error {
	return w.handler.NetworkCreateRAW(DB, username, password)
}

func (w *FederationHandlerWrapper) RegisterNode(resp http.ResponseWriter, req *http.Request) {
	w.handler.RegisterNode(resp, req)
}

func (w *FederationHandlerWrapper) ListNodes(resp http.ResponseWriter, req *http.Request) {
	w.handler.ListNodes(resp, req)
}

func (w *FederationHandlerWrapper) GetNode(resp http.ResponseWriter, req *http.Request) {
	w.handler.GetNode(resp, req)
}

func (w *FederationHandlerWrapper) WhitelistedPeers(resp http.ResponseWriter, req *http.Request) {
	w.handler.WhitelistedPeers(resp, req)
}

func (w *FederationHandlerWrapper) RequestNode(resp http.ResponseWriter, req *http.Request) {
	w.handler.RequestNode(resp, req)
}

func (w *FederationHandlerWrapper) RequestNodeByPeerId(resp http.ResponseWriter, req *http.Request) {
	w.handler.RequestNodeByPeerId(resp, req)
}

func (w *FederationHandlerWrapper) CreateAndStartProxy(resp http.ResponseWriter, req *http.Request) {
	w.handler.CreateAndStartProxy(resp, req)
}

func (w *FederationHandlerWrapper) ListProxies(resp http.ResponseWriter, req *http.Request) {
	w.handler.ListProxies(resp, req)
}

func (w *FederationHandlerWrapper) DeleteProxy(resp http.ResponseWriter, req *http.Request) {
	w.handler.DeleteProxy(resp, req)
}

func (w *FederationHandlerWrapper) ReloadDomainProxies(resp http.ResponseWriter, req *http.Request) {
	w.handler.ReloadDomainProxies(resp, req)
}

func (w *FederationHandlerWrapper) UploadBinary(resp http.ResponseWriter, req *http.Request) {
	w.handler.UploadBinary(resp, req)
}

func (w *FederationHandlerWrapper) RequestSelfUpdate(resp http.ResponseWriter, req *http.Request) {
	w.handler.RequestSelfUpdate(resp, req)
}

func (w *FederationHandlerWrapper) DownloadBinary(resp http.ResponseWriter, req *http.Request) {
	w.handler.DownloadBinary(resp, req)
}

func (w *FederationHandlerWrapper) GetHiveSetupScript(resp http.ResponseWriter, req *http.Request) {
	w.handler.GetHiveSetupScript(resp, req)
}

func (w *FederationHandlerWrapper) SyncGet(resp http.ResponseWriter, req *http.Request) {
	w.handler.SyncGet(resp, req)
}

func (w *FederationHandlerWrapper) WebTerminalHandler(resp http.ResponseWriter, req *http.Request) {
	w.handler.WebTerminalHandler(resp, req)
}

func (w *FederationHandlerWrapper) GetIdentity() api.Identity {
	realIdentity := w.handler.GetIdentity()
	return api.Identity{
		ConnectMultiadress: realIdentity.ConnectMultiadress,
	}
}

func (w *FederationHandlerWrapper) Host() host.Host {
	return w.handler.Host
}

func (w *FederationHandlerWrapper) RemoveExpiredProxies(DB *gorm.DB) error {
	return w.handler.RemoveExpiredProxies(DB)
}

func (w *FederationHandlerWrapper) StartEgressProxy(DB *gorm.DB, proxy database.Proxy, originNode, targetNode database.Node, originPort, networkName string, protocolID protocol.ID) {
	w.handler.StartEgressProxy(DB, proxy, originNode, targetNode, originPort, networkName, protocolID)
}

func (w *FederationHandlerWrapper) StartSSHProxy(port int, originPort string) {
	w.handler.StartSSHProxy(port, originPort)
}

func (w *FederationHandlerWrapper) AutoRemoveExpiredProxies(DB *gorm.DB) {
	w.handler.AutoRemoveExpiredProxies(DB)
}

func (w *FederationHandlerWrapper) AddNetworkPeerId(networkName, peerId string) {
	w.handler.AddNetworkPeerId(networkName, peerId)
}

func (w *FederationHandlerWrapper) GetNetworkPeerIds(networkName string) map[string]bool {
	return w.handler.GetNetworkPeerIds(networkName)
}

func (w *FederationHandlerWrapper) StartNetworkSyncProcess(DB *gorm.DB, networkName string) {
	w.handler.StartNetworkSyncProcess(DB, networkName)
}

func (w *FederationHandlerWrapper) CreateIncomingRequestStreamHandler(scheme, host, domain string, hostPort int, allowedPaths []string, networkName string) network.StreamHandler {
	return w.handler.CreateIncomingRequestStreamHandler(scheme, host, domain, hostPort, allowedPaths, networkName)
}

func (w *FederationHandlerWrapper) Gater() *api.WhitelistGater {
	// Convert the federation gater to api gater
	if w.handler.Gater != nil {
		return &api.WhitelistGater{}
	}
	return nil
}
