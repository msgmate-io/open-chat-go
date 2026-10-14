module backend

go 1.26.0

require github.com/urfave/cli/v3 v3.11.0

require golang.org/x/crypto v0.55.0

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/coder/websocket v1.8.15
	github.com/google/uuid v1.6.0
	github.com/hibiken/asynq v0.26.0
	github.com/hibiken/asynqmon v0.7.2
	github.com/kardianos/service v1.3.0
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	gorm.io/driver/postgres v1.5.11
	gorm.io/driver/sqlite v1.6.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/glebarez/go-sqlite v1.22.0 // indirect
	github.com/google/pprof v0.0.0-20260402051712-545e8a4df936 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.9.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-sqlite3 v1.14.49 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/redis/go-redis/v9 v9.14.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// gorm stuff
require (
	github.com/glebarez/sqlite v1.11.0
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	golang.org/x/text v0.41.0 // indirect
	gorm.io/gorm v1.31.2
	modernc.org/sqlite v1.55.0 // indirect
)
