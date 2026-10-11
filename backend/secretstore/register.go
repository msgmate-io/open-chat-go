package secretstore

// The local-master provider is always available as a key-based fallback so
// existing deployments that only set a master key (OPEN_CHAT_SEAL_KEY) keep
// working without a password. Password-based providers (tvault) register
// themselves with a higher priority and take precedence when enabled.
func init() {
	RegisterProvider(NewLocalMasterProvider(LocalMasterOptions{}))
}
