module backend

go 1.26.0

require github.com/urfave/cli/v3 v3.10.1

require golang.org/x/crypto v0.53.0

require github.com/msgmate-io/go-tool-interface v0.0.0

require github.com/msgmate-io/go-integration-interface v0.0.0

require github.com/msgmate-io/admin-db-managemnt-integration v0.0.0

require github.com/msgmate-io/mcp-integration v0.0.0

require github.com/msgmate-io/rest-api-tool-integration v0.0.0

require github.com/msgmate-io/ssh-integration v0.0.0

require (
	github.com/coder/websocket v1.8.15
	github.com/google/uuid v1.6.0
	github.com/hibiken/asynq v0.26.0
	github.com/hibiken/asynqmon v0.7.2
	github.com/swaggo/swag/v2 v2.0.0-rc5
	golang.org/x/term v0.44.0
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/driver/postgres v1.6.0
)

replace github.com/msgmate-io/go-tool-interface => ../clients/go_tool_interface

replace github.com/msgmate-io/go-integration-interface => ../clients/go_integration_interface

replace github.com/msgmate-io/admin-db-managemnt-integration => ../clients/integrations/admin_db_managemnt_integration

replace github.com/msgmate-io/mcp-integration => ../clients/integrations/mcp_integration

replace github.com/msgmate-io/rest-api-tool-integration => ../clients/integrations/rest_api_tool_integration

replace github.com/msgmate-io/ssh-integration => ../clients/integrations/ssh_integration

require (
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/glebarez/go-sqlite v1.22.0 // indirect
	github.com/go-openapi/jsonpointer v0.24.0 // indirect
	github.com/go-openapi/jsonreference v0.21.6 // indirect
	github.com/go-openapi/spec v0.22.6 // indirect
	github.com/go-openapi/swag/conv v0.27.0 // indirect
	github.com/go-openapi/swag/jsonname v0.26.1 // indirect
	github.com/go-openapi/swag/jsonutils v0.27.0 // indirect
	github.com/go-openapi/swag/loading v0.27.0 // indirect
	github.com/go-openapi/swag/stringutils v0.27.0 // indirect
	github.com/go-openapi/swag/typeutils v0.27.0 // indirect
	github.com/go-openapi/swag/yamlutils v0.27.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/redis/go-redis/v9 v9.21.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/sv-tools/openapi v0.4.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.73.5 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// gorm stuff
require (
	github.com/glebarez/sqlite v1.11.0
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	golang.org/x/text v0.38.0 // indirect
	gorm.io/gorm v1.25.12
	modernc.org/sqlite v1.53.0 // indirect
)
