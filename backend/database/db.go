package database

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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

	return db
}
