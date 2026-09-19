//go:build !gitintegration

package cmd

import (
	"fmt"

	"backend/runtimecfg"
	"gorm.io/gorm"
)

var gitBootstrapEnvKeys = []string{
	"OCI_GIT_BOOTSTRAP_TOKENS",
	"OCI_GIT_BOOTSTRAP_REPOSITORIES",
	"OCI_GIT_BOOTSTRAP_WORKSPACES",
	"OCI_GIT_BOOTSTRAP_WORKSPACE_GRANTS",
	"OCI_GIT_BOOTSTRAP_DEFAULT_OWNERS",
}

func applyGitBootstrapSources(_ *gorm.DB, _ string) error {
	for _, envKey := range gitBootstrapEnvKeys {
		if value, ok := runtimecfg.GetAll()[envKey]; ok && value.Value != "" {
			return fmt.Errorf("git bootstrap requested via %s, but git integration is not included in this build", envKey)
		}
	}
	return nil
}
