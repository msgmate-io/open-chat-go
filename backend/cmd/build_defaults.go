package cmd

// Build-time defaults for server configuration
// These variables can be set at build time using -ldflags
var (
	// Default bot credentials (format: "username:password")
	buildTimeDefaultBot = "bot:random"
)

// GetBuildTimeDefaultBot returns the build-time default bot credentials
func GetBuildTimeDefaultBot() string {
	return buildTimeDefaultBot
}
