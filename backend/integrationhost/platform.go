package integrationhost

import (
	"errors"
	"fmt"
	"strings"

	"backend/database"
	"backend/runtimecfg"
	"backend/server/util"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"

	"gorm.io/gorm"
)

// This file implements the HostVersion 4 platform surfaces: the write side of
// the resolved runtime configuration and local-account provisioning. Both are
// thin adapters over the existing core helpers so the interface module stays
// free of core types.

// configWriter adapts backend/runtimecfg's write helpers.
type configWriter struct{}

func (configWriter) SetValue(key, value string, sensitive bool) {
	runtimecfg.SetValue(key, runtimecfg.Value{Value: value, Sensitive: sensitive})
}

func (configWriter) DeleteValue(key string) { runtimecfg.DeleteValue(key) }

func (configWriter) PublicBaseURL() string { return runtimecfg.PublicBaseURL() }

// usersAdapter provisions local accounts. The storage handle arrives as
// interface{} (Storage.Handle) and is type-asserted here so the interface
// module does not depend on gorm.
type usersAdapter struct{}

func asDB(handle interface{}) (*gorm.DB, error) {
	db, ok := handle.(*gorm.DB)
	if !ok || db == nil {
		return nil, errors.New("integrationhost: invalid database handle")
	}
	return db, nil
}

func (usersAdapter) Create(handle interface{}, username, password string, isAdmin bool) (integrationinterface.User, error) {
	db, err := asDB(handle)
	if err != nil {
		return nil, err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("integrationhost: username is required")
	}
	if existing, ok, err := (usersAdapter{}).ByUsername(handle, username); err != nil {
		return nil, err
	} else if ok {
		return existing, nil
	}
	if password == "" {
		// CreateUserPwPreHashed with an empty hash mirrors an account that has
		// no usable password (bridge/service users sign in by other means).
		if err, created := util.CreateUserPwPreHashed(db, username, "", isAdmin); err != nil {
			return nil, err
		} else if created != nil {
			return user{u: created}, nil
		}
		return nil, fmt.Errorf("integrationhost: failed to create user %q", username)
	}
	if err, created := util.CreateUser(db, username, password, isAdmin); err != nil {
		return nil, err
	} else if created != nil {
		return user{u: created}, nil
	}
	return nil, fmt.Errorf("integrationhost: failed to create user %q", username)
}

func (usersAdapter) ByUsername(handle interface{}, username string) (integrationinterface.User, bool, error) {
	db, err := asDB(handle)
	if err != nil {
		return nil, false, err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, false, nil
	}
	row := database.User{}
	if err := db.Where("username = ? OR email = ?", username, username).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return user{u: &row}, true, nil
}

func (usersAdapter) SetAutomated(handle interface{}, userID uint, automated bool) error {
	db, err := asDB(handle)
	if err != nil {
		return err
	}
	if userID == 0 {
		return errors.New("integrationhost: user id is required")
	}
	return db.Model(&database.User{}).Where("id = ?", userID).Update("is_automated", automated).Error
}
