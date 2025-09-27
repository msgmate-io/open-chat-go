package cmd

import (
	"context"
	"fmt"
	"os"

	"backend/integrations"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

// applyIntegrationBootstrapHooks invokes the optional startup bootstrap hook
// declared by loaded integrations. Integrations that adopt
// integrationinterface.Definition.Bootstrap own their own bootstrap decoding
// instead of the core server hardcoding per-integration wiring and build tags.
func applyIntegrationBootstrapHooks(ctx context.Context, DB *gorm.DB, adminUsername string) error {
	for _, decl := range integrations.BootstrapDeclarations() {
		if decl.Bootstrap == nil {
			continue
		}
		sources := integrationinterface.BootstrapSources{
			DB:            DB,
			AdminUsername: adminUsername,
			Env:           os.LookupEnv,
		}
		if err := decl.Bootstrap(ctx, sources); err != nil {
			return fmt.Errorf("integration %s bootstrap: %w", decl.IntegrationName, err)
		}
	}
	return nil
}
