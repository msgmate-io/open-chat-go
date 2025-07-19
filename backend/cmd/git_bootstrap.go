//go:build gitintegration

package cmd

import (
	gitintegration "github.com/msgmate-io/git-integration"
	"gorm.io/gorm"
)

// applyGitBootstrapSources applies the git integration's runtime config
// bootstrap. All git bootstrap env keys (OCI_GIT_BOOTSTRAP_*) are declared and
// owned by the git integration itself and can be set either directly as env
// vars or through open-chat.json (integrations.git.bootstrap_*).
func applyGitBootstrapSources(DB *gorm.DB, fallbackOwner string) error {
	if _, err := gitintegration.ApplyRuntimeConfigBootstrap(DB, fallbackOwner); err != nil {
		return err
	}
	return nil
}
