//go:build !android

package database

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openSQLite(filePath string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(filePath), &gorm.Config{})
}
