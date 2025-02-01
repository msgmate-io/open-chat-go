package cmd

import "strings"

// Build-time defaults for server configuration
// These variables can be set at build time using -ldflags
var (
	// Default bot credentials (format: "username:password")
	buildTimeDefaultBot = "bot:password"

	// Default network credentials (format: "username:password", empty to disable)
	buildTimeDefaultNetworkCredentials = ""

	// Default bootstrap peers (comma-separated base64 encoded node info)
	buildTimeNetworkBootstrapPeers = ""
)

// GetBuildTimeDefaultBot returns the build-time default bot credentials
func GetBuildTimeDefaultBot() string {
	return buildTimeDefaultBot
}

// GetBuildTimeDefaultNetworkCredentials returns the build-time default network credentials
func GetBuildTimeDefaultNetworkCredentials() string {
	return buildTimeDefaultNetworkCredentials
}

// GetBuildTimeNetworkBootstrapPeers returns the build-time default bootstrap peers
func GetBuildTimeNetworkBootstrapPeers() string {
	return buildTimeNetworkBootstrapPeers
}

// GetBuildTimeNetworkBootstrapPeersSlice returns the build-time default bootstrap peers as a slice
func GetBuildTimeNetworkBootstrapPeersSlice() []string {
	if buildTimeNetworkBootstrapPeers == "" {
		return []string{}
	}
	// Split by comma and trim whitespace
	peers := strings.Split(buildTimeNetworkBootstrapPeers, ",")
	for i, peer := range peers {
		peers[i] = strings.TrimSpace(peer)
	}
	return peers
}
