package admin

import (
	"backend/database"
	"backend/server/util"
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"reflect"
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

	var model interface{}
	found := false
	for _, t := range database.Tabels {
		stmt := &gorm.Statement{DB: DB}
		stmt.Parse(t)
		if stmt.Schema.Table == tableName {
			// Create a new instance of the model type
			modelType := reflect.TypeOf(t)
			if modelType.Kind() == reflect.Ptr {
				model = reflect.New(modelType.Elem()).Interface()
			} else {
				model = reflect.New(modelType).Interface()
			}
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	// Use the model type to query the database
	query := DB.Table(tableName).Where("id = ?", id)

	// Check for preloads in the table configuration
	if config, exists := tableConfigurations[tableName]; exists {
		for _, preload := range config.Preloads {
			// Add the preload with the "?" option to ignore errors when the relationship doesn't exist
			query = query.Preload(preload, func(db *gorm.DB) *gorm.DB {
				return db.Unscoped() // This allows loading soft-deleted records too
			})
		}
	}

	// Debug: Log the query and ID
	fmt.Printf("Executing query on table: %s with ID: %d\n", tableName, id)

	// First try to find the record including soft-deleted ones
	unscoped := DB.Table(tableName).Unscoped().Where("id = ?", id)
	var exists bool
	unscoped.Select("1").Scan(&exists)

	if !exists {
		// Record truly doesn't exist
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	// Execute the normal query with soft-delete filtering
	err = query.First(model).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Record exists but was soft-deleted
			http.Error(w, "Record was deleted", http.StatusGone) // 410 Gone is more appropriate for deleted resources
		} else {
			// For other errors, return an internal server error
			http.Error(w, fmt.Sprintf("Error querying table: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Convert the model to a map for JSON encoding
	result := make(map[string]interface{})
	stmt := &gorm.Statement{DB: DB}
	stmt.Parse(model)
	stmt.ReflectValue = reflect.ValueOf(model)
	for _, field := range stmt.Schema.Fields {
		value, _ := field.ValueOf(context.Background(), stmt.ReflectValue)
		if field.DBName != "" {
			if field.PrimaryKey {
				result["id"] = value // Explicitly set the primary key field
			} else {
				result[field.DBName] = value
			}
		}
	}

	// Include preloaded data
	if config, exists := tableConfigurations[tableName]; exists {
		for _, preload := range config.Preloads {
			preloadValue := reflect.Indirect(stmt.ReflectValue).FieldByName(preload)
			if preloadValue.IsValid() {
				// Use the mapping if available, otherwise use the original name
				jsonKey := preload
				if mapping, ok := config.PreloadMappings[preload]; ok {
					jsonKey = mapping
				}

				// Check if the preloaded value is nil or zero
				if preloadValue.IsZero() || (preloadValue.Kind() == reflect.Ptr && preloadValue.IsNil()) {
					result[jsonKey] = nil
				} else if preloadValue.CanInterface() {
					result[jsonKey] = preloadValue.Interface()
				} else {
					result[jsonKey] = nil
				}
			} else {
				// If the field doesn't exist, set it to nil
				jsonKey := preload
				if mapping, ok := config.PreloadMappings[preload]; ok {
					jsonKey = mapping
				}
				result[jsonKey] = nil
			}
		}
	}

	// Add JSON field parsing
	if config, exists := tableConfigurations[tableName]; exists && len(config.JsonFields) > 0 {
		for _, jsonFieldName := range config.JsonFields {
			field := stmt.Schema.LookUpField(jsonFieldName)
			if field == nil {
				continue // Field not found in schema
			}

			// Get the raw value
			rawValue, isZero := field.ValueOf(context.Background(), stmt.ReflectValue)
			if isZero || rawValue == nil {
				continue // Skip nil values
			}

			// Handle different types of JSON fields based on their Go type
			switch v := rawValue.(type) {
			case []byte:
				// For []byte fields (like Config), try to parse as JSON
				if len(v) > 0 {
					var jsonData interface{}
					if err := json.Unmarshal(v, &jsonData); err == nil {
						// If parsing successful, use the parsed JSON
						result[field.DBName] = jsonData
					}
				}
			case json.RawMessage:
				// For json.RawMessage fields
				var jsonData interface{}
				if err := json.Unmarshal(v, &jsonData); err == nil {
					result[field.DBName] = jsonData
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
