package integrations

import (
	"backend/database"
	"sort"

	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"
	"gorm.io/gorm"
)

func IsVisibleToUser(DB *gorm.DB, def extiface.Definition, user *database.User) (bool, error) {
	if user == nil {
		return false, nil
	}
	if user.IsAdmin {
		return true, nil
	}
	if def.AdminOnly {
		return false, nil
	}
	if def.UserAccessible {
		return true, nil
	}
	return false, nil
}

func IsVisibleByName(DB *gorm.DB, user *database.User, integrationName string) (bool, error) {
	def, found := Get(integrationName)
	if !found {
		return false, nil
	}
	return IsVisibleToUser(DB, def, user)
}

func ListVisibleDefinitions(DB *gorm.DB, user *database.User) ([]extiface.Definition, error) {
	defs := List()
	out := make([]extiface.Definition, 0, len(defs))
	for _, def := range defs {
		visible, err := IsVisibleToUser(DB, def, user)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func EnsureDefaultAdminIntegrationAccess(DB *gorm.DB) error {
	if DB == nil {
		return nil
	}
	adminOnlyNames := []string{}
	for _, def := range List() {
		if def.AdminOnly {
			adminOnlyNames = append(adminOnlyNames, def.Name)
		}
	}
	if len(adminOnlyNames) == 0 {
		return nil
	}

	admins := []database.User{}
	if err := DB.Where("is_admin = ?", true).Find(&admins).Error; err != nil {
		return err
	}
	for _, admin := range admins {
		for _, integrationName := range adminOnlyNames {
			if err := database.EnsureIntegrationAccess(DB, admin.ID, integrationName); err != nil {
				return err
			}
		}
	}
	return nil
}
