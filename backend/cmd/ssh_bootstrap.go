//go:build sshintegration

package cmd

import (
	"fmt"

	sshintegration "github.com/msgmate-io/ssh-integration"
	"gorm.io/gorm"
)

func applySSHBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, keySpecs []string, serverSpecs []string, keyGrantSpecs []string, serverGrantSpecs []string) error {
	allKeySpecs := make([]sshintegration.BootstrapKeySpec, 0)
	for idx, spec := range keySpecs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return fmt.Errorf("add-ssh-keys-from-config[%d]: %w", idx, err)
		}
		decoded, err := decodeOneOrManyJSON[sshintegration.BootstrapKeySpec](raw, source)
		if err != nil {
			return fmt.Errorf("add-ssh-keys-from-config[%d]: %w", idx, err)
		}
		allKeySpecs = append(allKeySpecs, decoded...)
	}

	allServerSpecs := make([]sshintegration.BootstrapServerSpec, 0)
	for idx, spec := range serverSpecs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return fmt.Errorf("add-ssh-servers-from-config[%d]: %w", idx, err)
		}
		decoded, err := decodeOneOrManyJSON[sshintegration.BootstrapServerSpec](raw, source)
		if err != nil {
			return fmt.Errorf("add-ssh-servers-from-config[%d]: %w", idx, err)
		}
		allServerSpecs = append(allServerSpecs, decoded...)
	}

	allKeyGrantSpecs := make([]sshintegration.BootstrapKeyGrantSpec, 0)
	for idx, spec := range keyGrantSpecs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return fmt.Errorf("add-ssh-key-grants-from-config[%d]: %w", idx, err)
		}
		decoded, err := decodeOneOrManyJSON[sshintegration.BootstrapKeyGrantSpec](raw, source)
		if err != nil {
			return fmt.Errorf("add-ssh-key-grants-from-config[%d]: %w", idx, err)
		}
		allKeyGrantSpecs = append(allKeyGrantSpecs, decoded...)
	}

	allServerGrantSpecs := make([]sshintegration.BootstrapServerGrantSpec, 0)
	for idx, spec := range serverGrantSpecs {
		raw, source, err := resolveBootstrapSpecBytes(spec)
		if err != nil {
			return fmt.Errorf("add-ssh-server-grants-from-config[%d]: %w", idx, err)
		}
		decoded, err := decodeOneOrManyJSON[sshintegration.BootstrapServerGrantSpec](raw, source)
		if err != nil {
			return fmt.Errorf("add-ssh-server-grants-from-config[%d]: %w", idx, err)
		}
		allServerGrantSpecs = append(allServerGrantSpecs, decoded...)
	}

	if len(allKeySpecs) == 0 && len(allServerSpecs) == 0 && len(allKeyGrantSpecs) == 0 && len(allServerGrantSpecs) == 0 {
		return nil
	}

	_, err := sshintegration.ApplyBootstrap(DB, sshintegration.BootstrapSpec{
		FallbackOwner: fallbackOwner,
		DefaultOwners: normalizeOwnersList(defaultOwners),
		Keys:          allKeySpecs,
		Servers:       allServerSpecs,
		KeyGrants:     allKeyGrantSpecs,
		ServerGrants:  allServerGrantSpecs,
	})
	if err != nil {
		return err
	}

	return nil
}
