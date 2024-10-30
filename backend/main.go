package main

/*
Notes:

- maybe introduce for rest documentation https://github.com/swaggest/rest/
- open-chat's python api spec: https://beta.msgmate.io/api/schema/swagger-ui/
- openapi choices: https://www.reddit.com/r/golang/comments/1avsog1/go_openapi_codegen/
- `$HOME/go/bin/swag init`

*/

import (
	"backend/Api"
	"backend/Models"
	"fmt"
	"net/http"
)

const serverPort int = 3000
const dbBackend Models.DbBackend = Models.SqLite

// @title Open-Chat 2.0 API
// @version 3.1
// @description Hello there :)
// @termsOfService http://swagger.io/terms/

// @host beta.msgmate.io
// @BasePath /v2
func main() {
	// 1 - Setup the database
	Models.SetupDatabase(dbBackend)

	// 2 - Setup the router
	r := Api.SetupRouting()

	fmt.Printf("Server running on http://localhost:%d\n", serverPort)
	http.ListenAndServe(fmt.Sprintf(":%d", serverPort), r)
}
