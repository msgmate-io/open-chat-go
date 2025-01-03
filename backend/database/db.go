package database

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"log"
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

	stmt := &gorm.Statement{DB: db}
	if debug {
		for i, table := range Tabels {
			stmt.Parse(table)
			tableName := stmt.Schema.Table
			log.Println(fmt.Sprintf("Dropping tables (%v/%v): %v", i+1, len(Tabels), tableName))
			db.Migrator().DropTable(table)
		}
	}

	for i, table := range Tabels {
		stmt.Parse(table)
		tableName := stmt.Schema.Table
		log.Println(fmt.Sprintf("Migrating table (%v/%v): %v", i+1, len(Tabels), tableName))
		err = db.AutoMigrate(table)
		if err != nil {
			panic(fmt.Sprintf("Failed to migrate table: %v", err))
		}
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
			log.Println(fmt.Sprintf("Created test user: '%v'", user.Name))
		}
	}

	contact, err := users[0].AddContact(DB, users[1])
	if err != nil {
		panic(fmt.Sprintf("Failed to create test contact: %v", err))
	} else {
		log.Println(fmt.Sprintf("Created test contact: '%v (owner) <-> %v'", users[contact.OwningUserId-1].Name, users[contact.ContactUserId-1].Name))
	}
}
