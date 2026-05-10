package database

import (
	"fmt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"sync"

	"backend/logging"
)

type DBConfig struct {
	Backend  string // "sqlite" or "postgres"
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
	FilePath string // for SQLite
	Debug    bool
	ResetDB  bool
	// SkipMigrations disables the automatic schema migration pass. Set it on
	// secondary processes (e.g. extra workers) so they do not race the primary
	// server's AutoMigrate with concurrent DDL.
	SkipMigrations bool
}

// globalDB is set once the process-wide primary database handle is ready.
// Components that run outside an HTTP request context (queue tasks, bot reply
// workers, integration hooks) can use GetGlobalDB to obtain it.
var (
	globalDBMu sync.RWMutex
	globalDB   *gorm.DB
)

// SetGlobalDB records the primary database handle for process-wide access.
func SetGlobalDB(db *gorm.DB) {
	globalDBMu.Lock()
	defer globalDBMu.Unlock()
	globalDB = db
}

// GetGlobalDB returns the primary database handle recorded via SetGlobalDB,
// or nil if none has been set yet.
func GetGlobalDB() *gorm.DB {
	globalDBMu.RLock()
	defer globalDBMu.RUnlock()
	return globalDB
}

func SetupDatabase(config DBConfig) *gorm.DB {
	var db *gorm.DB
	var err error

	switch config.Backend {
	case "sqlite":
		db, err = openSQLite(config.FilePath)
	case "postgres":
		db, err = gorm.Open(postgres.Open(config.FilePath), &gorm.Config{Logger: gormLoggerFor(false)})
	default:
		panic(fmt.Sprintf("Unsupported database backend: %s", config.Backend))
	}

	if err != nil {
		panic(fmt.Sprintf("Failed to connect to database: %v", err))
	}

	stmt := &gorm.Statement{DB: db}
	if config.SkipMigrations {
		logging.Infof("Skipping database migrations (SkipMigrations=true)")
		return db
	}

	if config.ResetDB {
		for i, migration := range Migrations {
			if tableMigration, ok := migration.(TableMigration); ok {
				stmt.Parse(tableMigration.Model)
				tableName := stmt.Schema.Table
				logging.Debugf("%v", fmt.Sprintf("Dropping tables (%v/%v): %v", i+1, len(Migrations), tableName))
				if tableName == "keys" {
					fmt.Println("NOT dropping keys table")
				} else {
					db.Migrator().DropTable(tableMigration.Model)
				}
			} else if _, ok := migration.(ChatAndMessageMigration); ok {
				logging.Debug("Dropping chat and message tables")
				db.Migrator().DropTable(&Chat{}, &Message{})
			}
		}
	}

	for i, migration := range Migrations {
		var tableName string
		if tableMigration, ok := migration.(TableMigration); ok {
			stmt.Parse(tableMigration.Model)
			tableName = stmt.Schema.Table
			logging.Debugf("Attempting to migrate table (%v/%v): %v", i+1, len(Migrations), tableName)
		} else {
			tableName = fmt.Sprintf("custom migration %T", migration)
			logging.Debugf("Attempting to run custom migration (%v/%v): %v", i+1, len(Migrations), tableName)
		}

		err = migration.Migrate(db)
		if err != nil {
			logging.Errorf("Migration failed for %s", tableName)
			panic(fmt.Sprintf("Failed to migrate %s: %v", tableName, err))
		}
		logging.Debugf("Successfully migrated: %s", tableName)
	}

	return db
}

func SetupTestUsers(DB *gorm.DB) {
	var users []*User
	for i, email := range []string{"tim+test1@timschupp.de", "tim+test2@timschupp.de"} {
		user, err := RegisterUser(DB, fmt.Sprintf("Test-User-%v", i+1), email, []byte("password"))
		users = append(users, user)
		if err != nil {
			panic(fmt.Sprintf("Failed to create test user: %v", err))
		} else {
			logging.Infof("%v", fmt.Sprintf("Created test user: '%v'", user.Name))
		}
	}

	contact, err := users[0].AddContact(DB, users[1])
	if err != nil {
		panic(fmt.Sprintf("Failed to create test contact: %v", err))
	} else {
		logging.Infof("%v", fmt.Sprintf("Created test contact: '%v (owner) <-> %v'", users[contact.OwningUserId-1].Name, users[contact.ContactUserId-1].Name))
	}
}
