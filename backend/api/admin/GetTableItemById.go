package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"strconv"
)

func GetTableItemById(w http.ResponseWriter, r *http.Request) {
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

	idStr := r.PathValue("id")
	if idStr == "" {
		http.Error(w, "ID is required", http.StatusBadRequest)
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid ID format", http.StatusBadRequest)
		return
	}

	// Find the corresponding model in the Tabels slice
	// var model interface{}
	found := false
	for _, t := range database.Tabels {
		stmt := &gorm.Statement{DB: DB}
		stmt.Parse(t)
		if stmt.Schema.Table == tableName {
			// model = t
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	// Query the database for the specific record
	rows, err := DB.Table(tableName).Where("id = ?", id).Rows()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying table: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting columns: %v", err), http.StatusInternalServerError)
		return
	}

	// Check if we found any rows
	if !rows.Next() {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	// Prepare value holders
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range columns {
		valuePtrs[i] = &values[i]
	}

	// Scan the row
	err = rows.Scan(valuePtrs...)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error scanning row: %v", err), http.StatusInternalServerError)
		return
	}

	// Create a map for the row
	result := make(map[string]interface{})
	for i, col := range columns {
		val := values[i]
		if val != nil {
			result[col] = val
		} else {
			result[col] = nil
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
