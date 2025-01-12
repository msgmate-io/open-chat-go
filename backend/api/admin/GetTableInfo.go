package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
)

type FieldInfo struct {
	Name       string `json:"name"`
	NameRaw    string `json:"name_raw"`
	Type       string `json:"type"`
	IsPrimary  bool   `json:"is_primary"`
	IsNullable bool   `json:"is_nullable"`
	Tag        string `json:"tag"`
}

type TableInfo struct {
	Name   string      `json:"name"`
	Fields []FieldInfo `json:"fields"`
}

type TableInfoConfig struct {
	IncludeFields []string
}

var tableConfigurations = map[string]TableInfoConfig{
	"users": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "Name", "Email", "Password", "Role"},
	},
	"messages": {
		IncludeFields: []string{"UUID", "ID", "CreatedAt", "DeletedAt", "SenderId", "ReceiverId", "DataType", "ChatId", "Text"},
	},
}

func GetTableInfo(w http.ResponseWriter, r *http.Request) {
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
	var model interface{}
	found := false
	for _, t := range database.Tabels {
		stmt := &gorm.Statement{DB: DB}
		stmt.Parse(t)
		if stmt.Schema.Table == tableName {
			model = t
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	// Get schema information
	stmt := &gorm.Statement{DB: DB}
	stmt.Parse(model)

	fields := make([]FieldInfo, 0)
	for _, field := range stmt.Schema.Fields {
		// Check if we have a configuration for this table
		if config, exists := tableConfigurations[tableName]; exists {
			// Only include field if it's in the IncludeFields list
			include := false
			for _, allowedField := range config.IncludeFields {
				if field.Name == allowedField {
					include = true
					break
				}
			}
			if !include {
				continue
			}
		}

		fields = append(fields, FieldInfo{
			Name:       field.Name,
			NameRaw:    field.DBName,
			Type:       string(field.DataType),
			IsPrimary:  field.PrimaryKey,
			IsNullable: !field.NotNull,
			Tag:        string(field.TagSettings["JSON"]),
		})
	}

	tableInfo := TableInfo{
		Name:   tableName,
		Fields: fields,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tableInfo)
}
