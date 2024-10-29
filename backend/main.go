package main

/*
Notes:

- maybe introduce for rest documentation https://github.com/swaggest/rest/
- open-chat's python api spec: https://beta.msgmate.io/api/schema/swagger-ui/

*/

import (
	"backend/Api"
	"backend/Models"
	"fmt"
	"net/http"
)

const serverPort int = 3000
const dbBackend Models.DbBackend = Models.SqLite

func main() {
	// 1 - Setup the database
	Models.SetupDatabase(dbBackend)

	// 2 - Setup the router
	r := Api.SetupRouting()
	fmt.Printf("Server running on port %d\n", serverPort)
	http.ListenAndServe(fmt.Sprintf(":%d", serverPort), r)
}
