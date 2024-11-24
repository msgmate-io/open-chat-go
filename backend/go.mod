module backend

go 1.23.2

require github.com/urfave/cli/v3 v3.0.0-alpha9.3

require github.com/rs/cors v1.11.1

require golang.org/x/crypto v0.29.0

require github.com/google/uuid v1.6.0

require github.com/coder/websocket v1.8.12 // indirect

// gorm stuff
require (
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-sqlite3 v1.14.24 // indirect
	golang.org/x/text v0.20.0 // indirect
	gorm.io/driver/sqlite v1.5.6
	gorm.io/gorm v1.25.12
)
