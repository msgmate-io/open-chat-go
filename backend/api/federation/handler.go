package federation

import (
	"context"
	"github.com/libp2p/go-libp2p/core/host"
)

type FederationHandler struct {
	Host      host.Host
	AutoPings map[string]context.CancelFunc
	Gater     *WhitelistGater
	// port -> service_uuid map TODO
}
