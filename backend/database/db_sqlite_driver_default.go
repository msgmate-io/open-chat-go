//go:build !android

package database

import (
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// busy_timeout makes concurrent writers wait for the single SQLite write lock
// instead of failing immediately with SQLITE_BUSY ("database is locked"); WAL
// lets readers proceed while a writer is active. Both are needed because
// startup seeding and background workers can touch the database concurrently.
const sqliteDSNPragmas = "_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"

func sqliteDSN(filePath string) string {
	if filePath == "" || strings.Contains(filePath, ":memory:") {
		return filePath
	}
	if strings.Contains(filePath, "?") {
		return filePath + "&" + sqliteDSNPragmas
	}
	return filePath + "?" + sqliteDSNPragmas
}

func openSQLite(filePath string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(sqliteDSN(filePath)), &gorm.Config{})
}
