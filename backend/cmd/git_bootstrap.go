//go:build gitintegration

package cmd

import (
	"fmt"

	gitintegration "github.com/msgmate-io/git-integration"
	"gorm.io/gorm"
)

// decodeGitBootstrapSpecs decodes one-or-many bootstrap specs for a given git
// bootstrap section, mirroring the ssh/opencode bootstrap wiring.
func decodeGitBootstrapSpecs[T any](label string, specs []string) ([]T, error) {
	all := make([]T, 0)
	for idx, spec := range specs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", label, idx, err)
		}
		decoded, err := decodeOneOrManyJSON[T](raw, source)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", label, idx, err)
		}
		all = append(all, decoded...)
	}
	return all, nil
}

// applyGitBootstrapSources applies the git integration's bootstrap sources:
// the backend-level `bootstrap.git` section (decoded here, including
// `bootstrap.git.triggers`) plus the integration-owned OCI_GIT_BOOTSTRAP_* env
// keys, which are also settable through open-chat.json
// (integrations.git.bootstrap_*, including bootstrap_triggers). Both sources
// are idempotent, so applying them together is safe.
func applyGitBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, tokenSpecs []string, repositorySpecs []string, workspaceSpecs []string, workspaceGrantSpecs []string, triggerSpecs []string) error {
	allTokenSpecs, err := decodeGitBootstrapSpecs[gitintegration.BootstrapTokenSpec]("bootstrap.git.tokens", tokenSpecs)
	if err != nil {
		return err
	}
	allRepositorySpecs, err := decodeGitBootstrapSpecs[gitintegration.BootstrapRepositorySpec]("bootstrap.git.repositories", repositorySpecs)
	if err != nil {
		return err
	}
	allWorkspaceSpecs, err := decodeGitBootstrapSpecs[gitintegration.BootstrapWorkspaceSpec]("bootstrap.git.workspaces", workspaceSpecs)
	if err != nil {
		return err
	}
	allWorkspaceGrantSpecs, err := decodeGitBootstrapSpecs[gitintegration.BootstrapWorkspaceGrantSpec]("bootstrap.git.workspace_grants", workspaceGrantSpecs)
	if err != nil {
		return err
	}
	allTriggerSpecs, err := decodeGitBootstrapSpecs[gitintegration.BootstrapTriggerSpec]("bootstrap.git.triggers", triggerSpecs)
	if err != nil {
		return err
	}

	if len(allTokenSpecs) > 0 || len(allRepositorySpecs) > 0 || len(allWorkspaceSpecs) > 0 || len(allWorkspaceGrantSpecs) > 0 || len(allTriggerSpecs) > 0 || len(normalizeOwnersList(defaultOwners)) > 0 {
		if _, err := gitintegration.ApplyBootstrap(DB, gitintegration.BootstrapSpec{
			FallbackOwner:   fallbackOwner,
			DefaultOwners:   normalizeOwnersList(defaultOwners),
			Tokens:          allTokenSpecs,
			Repositories:    allRepositorySpecs,
			Workspaces:      allWorkspaceSpecs,
			WorkspaceGrants: allWorkspaceGrantSpecs,
			Triggers:        allTriggerSpecs,
		}); err != nil {
			return err
		}
	}

	// integration-owned OCI_GIT_BOOTSTRAP_* env sources (idempotent)
	if _, err := gitintegration.ApplyRuntimeConfigBootstrap(DB, fallbackOwner); err != nil {
		return err
	}
	return nil
}
