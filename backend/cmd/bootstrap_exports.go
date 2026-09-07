package cmd

import (
	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

func ApplyBotBootstrapConfigFiles(DB *gorm.DB, paths []string, validateStrength bool) error {
	return applyBotBootstrapConfigFiles(DB, paths, validateStrength)
}

func ApplyIntegrationBotBootstrapConfigs(DB *gorm.DB, sourcePrefix string, configs []extiface.BotBootstrapConfig, validateStrength bool) error {
	return applyIntegrationBotBootstrapConfigs(DB, sourcePrefix, configs, validateStrength)
}

func SyncBotsInheritingDefaultModelAccess(DB *gorm.DB, defaultBotUsername string, configs []extiface.BotBootstrapConfig) error {
	return syncBotsInheritingDefaultModelAccess(DB, defaultBotUsername, configs)
}

func ApplySSHBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, keySpecs []string, serverSpecs []string) error {
	return applySSHBootstrapSources(DB, fallbackOwner, defaultOwners, keySpecs, serverSpecs, nil, nil)
}

func ApplySSHBootstrapSourcesWithGrants(DB *gorm.DB, fallbackOwner string, defaultOwners []string, keySpecs []string, serverSpecs []string, keyGrantSpecs []string, serverGrantSpecs []string) error {
	return applySSHBootstrapSources(DB, fallbackOwner, defaultOwners, keySpecs, serverSpecs, keyGrantSpecs, serverGrantSpecs)
}

func ApplyOpencodeBootstrapSources(DB *gorm.DB, fallbackOwner string, defaultOwners []string, projectSpecs []string) error {
	return applyOpencodeBootstrapSources(DB, fallbackOwner, defaultOwners, projectSpecs)
}
