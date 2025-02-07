package admin

import (
	"backend/database"
	"backend/server/util"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"strconv"
)

func DeleteTableItemById(w http.ResponseWriter, r *http.Request) {
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

	// Delete the record
	result := DB.Table(tableName).Where("id = ?", id).Delete(nil)
	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Error deleting record: %v", result.Error), http.StatusInternalServerError)
		return
	}

	if result.RowsAffected == 0 {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}
