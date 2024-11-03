package main

/*

Notes:

- maybe introduce for rest documentation https://github.com/swaggest/rest/
- open-chat's python api spec: https://beta.msgmate.io/api/schema/swagger-ui/
- openapi choices: https://www.reddit.com/r/golang/comments/1avsog1/go_openapi_codegen/
- use token based authentication for now: github.com/go-chi/jwtauth
- add csrf potection later https://github.com/francoposa/go-csrf-examples
- Generate api schema `$HOME/go/bin/swag init`
- https://github.com/tauri-apps/tauri
- https://github.com/swaggo/swag/tree/v2

*/

import (
	"backend/Api"
	"backend/Models"
	"fmt"
	"net/http"
)

const serverPort int = 3000
const Debug bool = true
const dbBackend Models.DbBackend = Models.SqLite

// @title Open-Chat 2.0 API
// @version 3.1
// @description Hello there :)
// @termsOfService http://swagger.io/terms/

// @host localhost:3000
// @BasePath /
func main() {
	// 1 - Setup the database
	Models.SetupDatabase(dbBackend)

	// 2 - Setup the router
	r := Api.SetupRouting(serverPort)

	// 3 - Serve the API
	fmt.Printf("Server running on http://localhost:%d\n", serverPort)
	http.ListenAndServe(fmt.Sprintf(":%d", serverPort), r)
}
