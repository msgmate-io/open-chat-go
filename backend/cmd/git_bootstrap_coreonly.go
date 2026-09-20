//go:build !gitintegration

package cmd

import (
	"fmt"
	"strings"

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

func hasNonEmptyGitBootstrapSpec(specs ...[]string) bool {
	for _, group := range specs {
		for _, spec := range group {
			if strings.TrimSpace(spec) != "" {
				return true
			}
		}
	}
	return false
}

func applyGitBootstrapSources(_ *gorm.DB, _ string, defaultOwners []string, tokenSpecs []string, repositorySpecs []string, workspaceSpecs []string, workspaceGrantSpecs []string) error {
	for _, envKey := range gitBootstrapEnvKeys {
		if value, ok := runtimecfg.GetAll()[envKey]; ok && value.Value != "" {
			return fmt.Errorf("git bootstrap requested via %s, but git integration is not included in this build", envKey)
		}
	}
	if hasNonEmptyGitBootstrapSpec(defaultOwners, tokenSpecs, repositorySpecs, workspaceSpecs, workspaceGrantSpecs) {
		return fmt.Errorf("git bootstrap requested via bootstrap.git, but git integration is not included in this build")
	}
	return nil
}
