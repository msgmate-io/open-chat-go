module backend

go 1.23.2

require github.com/urfave/cli/v3 v3.0.0-alpha9.3

require github.com/rs/cors v1.11.1

require (
	github.com/alexedwards/scs/gormstore v0.0.0-20240316134038-7e11d57e8885 // indirect
	github.com/alexedwards/scs/v2 v2.8.0 // indirect
)

// gorm stuff
require (
	github.com/jinzhu/inflection v1.0.0
	github.com/jinzhu/now v1.1.5
	github.com/mattn/go-sqlite3 v1.14.24
	golang.org/x/text v0.20.0
	gorm.io/driver/sqlite v1.5.6
	gorm.io/gorm v1.25.12
)
