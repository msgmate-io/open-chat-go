package database

import (
	"fmt"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"log"
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
}

func SetupDatabase(config DBConfig) *gorm.DB {
	var db *gorm.DB
	var err error

	switch config.Backend {
	case "sqlite":
		db, err = gorm.Open(sqlite.Open(config.FilePath), &gorm.Config{})
	case "postgres":
		db, err = gorm.Open(postgres.Open(config.FilePath), &gorm.Config{})
	default:
		panic(fmt.Sprintf("Unsupported database backend: %s", config.Backend))
	}

	if err != nil {
		panic(fmt.Sprintf("Failed to connect to database: %v", err))
	}

	stmt := &gorm.Statement{DB: db}
	if config.ResetDB {
		for i, migration := range Migrations {
			if tableMigration, ok := migration.(TableMigration); ok {
				stmt.Parse(tableMigration.Model)
				tableName := stmt.Schema.Table
				log.Println(fmt.Sprintf("Dropping tables (%v/%v): %v", i+1, len(Migrations), tableName))
				if tableName == "keys" {
					fmt.Println("NOT dropping keys table")
				} else {
					db.Migrator().DropTable(tableMigration.Model)
				}
			} else if _, ok := migration.(ChatAndMessageMigration); ok {
				log.Println("Dropping chat and message tables")
				db.Migrator().DropTable(&Chat{}, &Message{})
			}
		}
	}

	for i, migration := range Migrations {
		var tableName string
		if tableMigration, ok := migration.(TableMigration); ok {
			stmt.Parse(tableMigration.Model)
			tableName = stmt.Schema.Table
			log.Printf("Attempting to migrate table (%v/%v): %v", i+1, len(Migrations), tableName)
		} else {
			tableName = fmt.Sprintf("custom migration %T", migration)
			log.Printf("Attempting to run custom migration (%v/%v): %v", i+1, len(Migrations), tableName)
		}

		err = migration.Migrate(db)
		if err != nil {
			log.Printf("Migration failed for %s", tableName)
			panic(fmt.Sprintf("Failed to migrate %s: %v", tableName, err))
		}
		log.Printf("Successfully migrated: %s", tableName)
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
