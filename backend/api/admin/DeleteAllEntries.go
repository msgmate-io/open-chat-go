package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"net/http"
)

type DeleteAllEntriesResponse struct {
	Message      string `json:"message"`
	RowsAffected int64  `json:"rows_affected"`
}

func DeleteAllEntries(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	tableName := r.PathValue("table_name")
	if tableName == "" {
		http.Error(w, "Table name is required", http.StatusBadRequest)
		return
	}

	// Validate that the table exists in the Tabels slice
	found := false
	for _, t := range database.Tabels {
		stmt := &gorm.Statement{DB: DB}
		stmt.Parse(t)
		if stmt.Schema.Table == tableName {
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	// Delete all entries from the specified table using WHERE 1=1 to match all records
	result := DB.Table(tableName).Where("1 = 1").Delete(nil)
	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Error deleting entries: %v", result.Error), http.StatusInternalServerError)
		return
	}

	response := DeleteAllEntriesResponse{
		Message:      fmt.Sprintf("Successfully deleted all entries from table '%s'", tableName),
		RowsAffected: result.RowsAffected,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
