package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"math"
	"net/http"
	"strconv"
)

type PaginatedTableData struct {
	database.Pagination
	Rows []map[string]interface{} `json:"rows"`
}

func GetTableDataPaginated(w http.ResponseWriter, r *http.Request) {
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

	// Setup pagination
	pagination := database.Pagination{Page: 1, Limit: 10}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	// First get total count for pagination
	var totalRows int64
	if err := DB.Table(tableName).Count(&totalRows).Error; err != nil {
		http.Error(w, fmt.Sprintf("Error counting rows: %v", err), http.StatusInternalServerError)
		return
	}

	pagination.TotalRows = totalRows
	pagination.TotalPages = int(math.Ceil(float64(totalRows) / float64(pagination.Limit)))

	// Create a slice to hold the results
	var results []map[string]interface{}

	// Query the database with pagination
	query := DB.Table(tableName).
		Offset(pagination.GetOffset()).
		Limit(pagination.GetLimit()).
		Order(pagination.GetSort())

	// Check for preloads in the table configuration
	if config, exists := tableConfigurations[tableName]; exists {
		for _, preload := range config.Preloads {
			query = query.Preload(preload)
		}
	}

	rows, err := query.Rows()
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

	// Prepare value holders
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range columns {
		valuePtrs[i] = &values[i]
	}

	// Iterate through rows
	for rows.Next() {
		err := rows.Scan(valuePtrs...)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error scanning row: %v", err), http.StatusInternalServerError)
			return
		}

		// Create a map for this row
		entry := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			if val != nil {
				entry[col] = val
			} else {
				entry[col] = nil
			}
		}
		results = append(results, entry)
	}

	response := PaginatedTableData{
		Pagination: pagination,
		Rows:       results,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
