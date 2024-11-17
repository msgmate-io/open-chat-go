package database

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"log"
)

var DB *gorm.DB

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

	stmt := &gorm.Statement{DB: db}
	if debug {
		for i, table := range Tabels {
			stmt.Parse(table)
			tableName := stmt.Schema.Table
			log.Println(fmt.Sprintf("Dropping tables (%v/%v): %v", i, len(Tabels), tableName))
			db.Migrator().DropTable(table)
		}
	}

	for i, table := range Tabels {
		stmt.Parse(table)
		tableName := stmt.Schema.Table
		log.Println(fmt.Sprintf("Migrating table (%v/%v): %v", i, len(Tabels), tableName))
		err = db.AutoMigrate(table)
		if err != nil {
			panic(fmt.Sprintf("Failed to migrate table: %v", err))
		}
	}

	if debug {
		user, err := RegisterUser(db, "Test User", "tim+test@timschupp.de", []byte("password"))
		if err != nil {
			panic(fmt.Sprintf("Failed to create test user: %v", err))
		} else {
			log.Println(fmt.Sprintf("Created test user: %v", user))
		}
	}

	// SetupSessionManager(db)

	return db
}
