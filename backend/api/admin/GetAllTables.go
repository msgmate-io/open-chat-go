package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
)

type TableListItem struct {
	Name string `json:"name"`
}

func GetAllTables(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	tables := make([]TableListItem, 0)

	// Iterate through all tables in the Tabels slice
	for _, t := range database.Tabels {
		stmt := &gorm.Statement{DB: DB}
		stmt.Parse(t)
		tables = append(tables, TableListItem{
			Name: stmt.Schema.Table,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tables)
}
