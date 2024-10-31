package Models

import (
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type DbBackend string

const (
	SqLite   DbBackend = "sqlite"
	Postgres DbBackend = "postgres"
)

var DB *gorm.DB

func SetupDatabase(
	dbBackend DbBackend,
) *gorm.DB {
	var err error

	// 1 - Select a backend
	switch dbBackend {
	case SqLite:
		DB, err = gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	case Postgres:
		panic("Postgres not implemented yet")
	default:
		panic("Unsupported database backend")
	}

	if err != nil {
		panic("failed to connect database")
	}

	// 2 - Migrate the schema
	DB.AutoMigrate(&User{})

	// 3 - Seed the database
	DB.Create(&User{username: "admin", password_hash: "admin"})

	fmt.Println("Database setup complete...")

	return DB
}
