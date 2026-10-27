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
//
// ci#850: the deployment saw ~10s SQLITE_BUSY spikes under a large bot-reply
// backlog. A 10s busy_timeout is shorter than a contended write can hold the
// lock, so writers failed instead of waiting. The pragmas below widen that
// window and make WAL maintenance cheaper:
//
//   - busy_timeout(30000): wait up to 30s for the write lock instead of 10s;
//   - journal_mode(WAL): readers never block the writer;
//   - synchronous(NORMAL): safe with WAL, and avoids an fsync per commit;
//   - wal_autocheckpoint(1000): bound WAL growth automatically (~4MB);
//   - cache_size(-32768): 32MB page cache (negative = KiB) to keep hot
//     chat/message pages resident.
const sqliteDSNPragmas = "_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=wal_autocheckpoint(1000)&_pragma=cache_size(-32768)"

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
	return gorm.Open(sqlite.Open(sqliteDSN(filePath)), gormConfig())
}
