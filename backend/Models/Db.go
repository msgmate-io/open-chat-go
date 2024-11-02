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

func SetupDatabase(
	dbBackend DbBackend,
) *gorm.DB {
	var db *gorm.DB
	var err error

	switch dbBackend {
	case SqLite:
		db, err = gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	case Postgres:
		panic("Postgres not implemented yet")
	default:
		panic("Unsupported database backend")
	}

	if err != nil {
		panic("failed to connect database")
	}

	db.AutoMigrate(&User{})

	fmt.Println("Database setup complete...")

	return db
}
