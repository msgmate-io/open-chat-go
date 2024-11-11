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
var DB *gorm.DB

func SetupSessionManager(
	db *gorm.DB,
) {
	var err error
	SessionManager = scs.New()
	SessionManager.Store, err = gormstore.New(db)

	if err != nil {
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
		user, err := RegisterUser(db, "Test User", "tim+test@timschupp.de", []byte("password"))
		if err != nil {
			panic(fmt.Sprintf("Failed to create test user: %v", err))
		} else {
			log.Println("Created test user %v", user)
		}
	}

	SetupSessionManager(db)

	return db
}
