package federation

import (
	"backend/database"
	"context"
	"fmt"
	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"gorm.io/gorm"
)

type WhitelistGater struct {
	allowedPeers []peer.ID
	DB           *gorm.DB
}

// InterceptAccept implements connmgr.ConnectionGater.
func (g *WhitelistGater) InterceptAccept(addrs network.ConnMultiaddrs) (allow bool) {
	// Allow incoming connections from whitelisted peers
	/*
		remotePeer := addrs.RemoteMultiaddr()
		peerID, err := remotePeer.ValueForProtocol(multiaddr.P_P2P)
		fmt.Println("InterceptAccept: Peer ID:", peerID)
		if err != nil {
			fmt.Println("InterceptAccept: Error getting peer ID from multiaddr:", err)
			return false
		}

		p, err := peer.Decode(peerID)
		if err != nil {
			fmt.Println("InterceptAccept: Error decoding peer ID:", err)
			return false
		}

		fmt.Println("InterceptAccept: Checking if peer is allowed:", p, "Allowed peers:", g.allowedPeers)
		return g.CheckLimit(context.Background(), p)
	*/
	// TODO: It seems like it is never possible to determine a peerID in this check thous I cannot gate-it
	return true
}

// InterceptAddrDial implements connmgr.ConnectionGater.
func (g *WhitelistGater) InterceptAddrDial(p peer.ID, addr multiaddr.Multiaddr) (allow bool) {
	// Allow outgoing connections to whitelisted peers
	return g.CheckLimit(context.Background(), p)
}

// InterceptPeerDial implements connmgr.ConnectionGater.
func (g *WhitelistGater) InterceptPeerDial(p peer.ID) (allow bool) {
	// Allow outgoing connections to whitelisted peers
	return g.CheckLimit(context.Background(), p)
}

// InterceptSecured implements connmgr.ConnectionGater.
func (g *WhitelistGater) InterceptSecured(direction network.Direction, p peer.ID, addrs network.ConnMultiaddrs) (allow bool) {
	// Allow secured connections with whitelisted peers
	return g.CheckLimit(context.Background(), p)
}

// InterceptUpgraded implements connmgr.ConnectionGater.
func (g *WhitelistGater) InterceptUpgraded(conn network.Conn) (allow bool, reason control.DisconnectReason) {
	// Allow upgraded connections with whitelisted peers
	if g.CheckLimit(context.Background(), conn.RemotePeer()) {
		return true, 0
	}
	return false, control.DisconnectReason(100) // Custom reason code for non-whitelisted peer
}

func NewWhitelistGater(allowedPeers []peer.ID) *WhitelistGater {
	return &WhitelistGater{allowedPeers: allowedPeers}
}

func (g *WhitelistGater) CheckLimit(ctx context.Context, p peer.ID) bool {
	fmt.Println("Checking if peer is allowed:", p, "Allowed peers:", g.allowedPeers)
	for _, allowedPeer := range g.allowedPeers {
		if allowedPeer == p {
			fmt.Println("===> Peer is allowed")
			return true
		}
	}
	return false
}

func (g *WhitelistGater) AddAllowedPeer(p peer.ID) {
	// Check if already in cache
	for _, allowedPeer := range g.allowedPeers {
		if allowedPeer == p {
			return
		}
	}
	g.allowedPeers = append(g.allowedPeers, p)
	// fmt.Println("Added peer to whitelist:", p, "Allowed peers:", g.allowedPeers)
}

func (g *WhitelistGater) RemoveAllowedPeer(p peer.ID) {
	newAllowedPeers := make([]peer.ID, 0)
	for _, allowedPeer := range g.allowedPeers {
		if allowedPeer != p {
			newAllowedPeers = append(newAllowedPeers, allowedPeer)
		}
	}
	g.allowedPeers = newAllowedPeers
}

func (g *WhitelistGater) RefreshAllowedPeers() error {
	var nodes []database.Node
	if err := g.DB.Find(&nodes).Error; err != nil {
		return err
	}

	newAllowedPeers := make([]peer.ID, 0)
	for _, node := range nodes {
		if pID, err := peer.Decode(node.PeerID); err == nil {
			newAllowedPeers = append(newAllowedPeers, pID)
		}
	}

	g.allowedPeers = newAllowedPeers
	return nil
}
