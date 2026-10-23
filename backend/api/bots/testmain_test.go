package bots

import (
	"backend/database"
	"backend/integrations"
	"os"
	"testing"
)

// TestMain registers the compiled-in integrations' models before any test runs.
// Test helpers call database.SetupDatabase directly (they never go through
// cmd/server.go), so integration-owned tables such as mcp_integration_configs
// would otherwise be missing. This mirrors the startup order in
// backend/cmd/server.go and keeps the bots tests representative of production.
func TestMain(m *testing.M) {
	database.RegisterExternalModels(integrations.AdditionalModels()...)
	os.Exit(m.Run())
}
