package cmd

import (
	"fmt"

	mcpintegration "github.com/msgmate-io/mcp-integration"
	"gorm.io/gorm"
)

// decodeMCPServerSpecs decodes one-or-many MCP server bootstrap specs for a
// given bootstrap section, mirroring the git/ssh/opencode bootstrap wiring.
func decodeMCPServerSpecs(label string, specs []string) ([]mcpintegration.BootstrapServerSpec, error) {
	all := make([]mcpintegration.BootstrapServerSpec, 0)
	for idx, spec := range specs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", label, idx, err)
		}
		decoded, err := decodeOneOrManyJSON[mcpintegration.BootstrapServerSpec](raw, source)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", label, idx, err)
		}
		all = append(all, decoded...)
	}
	return all, nil
}

// applyMCPBootstrapSources applies the MCP integration's bootstrap sources: the
// backend-level `bootstrap.mcp` section (decoded here) plus the
// integration-owned OCI_MCP_BOOTSTRAP_* env keys, which are also settable
// through open-chat.json (integrations.mcp.bootstrap_*). Both sources are
// idempotent, so applying them together is safe.
func applyMCPBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, serverSpecs []string) error {
	allServerSpecs, err := decodeMCPServerSpecs("bootstrap.mcp.servers", serverSpecs)
	if err != nil {
		return err
	}

	if len(allServerSpecs) > 0 || len(normalizeOwnersList(defaultOwners)) > 0 {
		if _, err := mcpintegration.ApplyBootstrap(DB, mcpintegration.BootstrapSpec{
			FallbackOwner: fallbackOwner,
			DefaultOwners: normalizeOwnersList(defaultOwners),
			Servers:       allServerSpecs,
		}); err != nil {
			return err
		}
	}

	// integration-owned OCI_MCP_BOOTSTRAP_* env sources (idempotent)
	if _, err := mcpintegration.ApplyRuntimeConfigBootstrap(DB, fallbackOwner); err != nil {
		return err
	}
	return nil
}