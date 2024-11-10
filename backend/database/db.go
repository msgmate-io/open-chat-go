package database

import (
	"fmt"
	"github.com/alexedwards/scs/gormstore"
	"github.com/alexedwards/scs/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"log"
)

var SessionManager *scs.SessionManager

func SetupSessionManager(
	db *gorm.DB,
) {
	var err error
	SessionManager = scs.New()
	if SessionManager.Store, err = gormstore.New(db); err != nil {
		log.Fatal(err)
	}
}

func SetupDatabase(
	dbBackend string,
	dbPathSqlite string,
	debug bool,
) *gorm.DB {
	if dbBackend != "sqlite" {
		panic(fmt.Sprintf("Unsupported/Unimplemented database backend: %s", dbBackend))
	}

	db, err := gorm.Open(sqlite.Open(dbPathSqlite), &gorm.Config{})
	if err != nil {
		panic(fmt.Sprintf("Failed to connect to database: %v", err))
	}

	if debug {
		db.Migrator().DropTable(&User{})
	}

	db.AutoMigrate(&User{})
	if debug {
		db.Create(&User{Name: "Test User", Email: "tim+test@timschupp.de"})
	}

	SetupSessionManager(db)

	return db
}
