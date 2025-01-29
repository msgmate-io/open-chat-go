package federation

import (
	"backend/database"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	T1mHttpRequestProtocolID    = "/t1m-http-request/0.0.1"
	T1mNetworkTCPProxyProtoID   = "/t1m-tcp-tunnel/0.0.1"
	T1mNetworkJoinProtocolID    = "/t1m-network-join/0.0.1"
	T1mNetworkRequestProtocolID = "/t1m-network-request/0.0.1"
)

func HashPeerId(peerId string) string {
	// should create a hash of the traffic target
	hash := sha256.New()
	hash.Write([]byte(peerId))
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func CreateT1mTCPTunnelProtocolID(originPort string, originPeerId string, targetPort string, targetPeerId string) protocol.ID {
	// /t1m-tcp-tunnel-<origin_port>-<origin_peer_id>-<target_port>-<target_peer_id>/0.0.1
	oneBigString := fmt.Sprintf("%s-%s-%s-%s", originPort, originPeerId, targetPort, targetPeerId)
	return protocol.ID(fmt.Sprintf("/t1m-tcp-tunnel-%s/0.0.1", HashPeerId(oneBigString)))
}

type FederationHandler struct {
	Host               host.Host
	AutoPings          map[string]context.CancelFunc
	Gater              *WhitelistGater
	Networks           map[string]database.Network
	NetworkPeerIds     map[string]map[string]bool
	NetworkSyncs       map[string]context.CancelFunc
	NetworkSyncBlocker map[string]bool
	// port -> service_uuid map TODO
}

func (h *FederationHandler) GetNetworkPeerIds(networkName string) map[string]bool {
	if _, ok := h.NetworkPeerIds[networkName]; !ok {
		return map[string]bool{}
	}
	return h.NetworkPeerIds[networkName]
}

func (h *FederationHandler) AddNetworkPeerId(networkName string, peerId string) {
	//check if the network exists
	if _, ok := h.NetworkPeerIds[networkName]; !ok {
		h.NetworkPeerIds[networkName] = map[string]bool{}
	}
	h.NetworkPeerIds[networkName][peerId] = true
}
