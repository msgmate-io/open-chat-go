package cmd

import "gorm.io/gorm"

func ApplyBotBootstrapConfigFiles(DB *gorm.DB, paths []string, validateStrength bool) error {
	return applyBotBootstrapConfigFiles(DB, paths, validateStrength)
}

func ApplySSHBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, keySpecs []string, serverSpecs []string) error {
	return applySSHBootstrapSources(DB, fallbackOwner, defaultOwners, keySpecs, serverSpecs)
}
