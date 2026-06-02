package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"gorm.io/gorm"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
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
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Fields      []FieldInfo `json:"fields"`
}

var (
	modelDescriptionOnce  sync.Once
	modelDescriptionCache map[string]string
)

func loadModelDescriptions(DB *gorm.DB) map[string]string {
	modelDescriptionOnce.Do(func() {
		modelDescriptionCache = make(map[string]string)
		fset := token.NewFileSet()
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			return
		}

		databaseDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "database")
		databaseDir = filepath.Clean(databaseDir)
		if stat, err := os.Stat(databaseDir); err != nil || !stat.IsDir() {
			return
		}

		pattern := filepath.Join(databaseDir, "*.go")
		files, err := filepath.Glob(pattern)
		if err != nil {
			return
		}

		descriptionsByType := make(map[string]string)
		for _, filePath := range files {
			parsed, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
			if err != nil {
				continue
			}

			for _, decl := range parsed.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					continue
				}

				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, ok := typeSpec.Type.(*ast.StructType); !ok {
						continue
					}

					commentGroup := typeSpec.Doc
					if commentGroup == nil {
						commentGroup = genDecl.Doc
					}
					if commentGroup == nil {
						continue
					}

					description := strings.TrimSpace(commentGroup.Text())
					if description == "" {
						continue
					}
					descriptionsByType[typeSpec.Name.Name] = description
				}
			}
		}

		for _, model := range database.Tabels {
			stmt := &gorm.Statement{DB: DB}
			if err := stmt.Parse(model); err != nil || stmt.Schema == nil {
				continue
			}

			t := reflect.TypeOf(model)
			if t.Kind() == reflect.Ptr {
				t = t.Elem()
			}
			if t.Kind() != reflect.Struct {
				continue
			}

			if description, ok := descriptionsByType[t.Name()]; ok {
				modelDescriptionCache[stmt.Schema.Table] = description
			}
		}
	})

	return modelDescriptionCache
}

type TableInfoConfig struct {
	IncludeFields   []string
	Preloads        []string
	PreloadMappings map[string]string // Maps preload field names to JSON keys
	JsonFields      []string          // Fields containing JSON data that should be parsed
}

var tableConfigurations = map[string]TableInfoConfig{
	"users": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "Name", "Email", "Password", "Role"},
	},
	"messages": {
		IncludeFields: []string{"UUID", "ID", "CreatedAt", "DeletedAt", "SenderId", "ReceiverId", "DataType", "ChatId", "Text", "Reasoning", "MetaData"},
	},
	"chats": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "User1Id", "User2Id", "LatestMessageId", "SharedConfigId", "ChatType"},
		Preloads:      []string{"LatestMessage", "SharedConfig", "User1", "User2"},
		PreloadMappings: map[string]string{
			"LatestMessage": "latest_message",
			"SharedConfig":  "shared_config",
			"User1":         "user1",
			"User2":         "user2",
		},
	},
	"shared_chat_configs": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "ChatId", "ConfigData"},
		Preloads:      []string{"Chat"},
		PreloadMappings: map[string]string{
			"Chat": "chat",
		},
		JsonFields: []string{"ConfigData"},
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

	exhaustive := r.URL.Query().Get("full") == "1" || r.URL.Query().Get("full") == "true"

	fields := make([]FieldInfo, 0)
	for _, field := range stmt.Schema.Fields {
		if exhaustive {
			fieldType := string(field.DataType)
			if fieldType == "" {
				fieldType = "unknown"
			}
			fields = append(fields, FieldInfo{
				Name:       field.Name,
				NameRaw:    field.DBName,
				Type:       fieldType,
				IsPrimary:  field.PrimaryKey,
				IsNullable: !field.NotNull,
				Tag:        string(field.TagSettings["JSON"]),
			})
			continue
		}

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

			// Check if this is a JSON field
			fieldType := string(field.DataType)
			for _, jsonField := range config.JsonFields {
				if field.Name == jsonField {
					fieldType = "object" // Mark as object type for JSON fields
					break
				}
			}

			if fieldType == "" {
				fieldType = "unknown"
			}

			fields = append(fields, FieldInfo{
				Name:       field.Name,
				NameRaw:    field.DBName,
				Type:       fieldType,
				IsPrimary:  field.PrimaryKey,
				IsNullable: !field.NotNull,
				Tag:        string(field.TagSettings["JSON"]),
			})
		} else {
			fieldType := string(field.DataType)
			if fieldType == "" {
				fieldType = "unknown"
			}
			fields = append(fields, FieldInfo{
				Name:       field.Name,
				NameRaw:    field.DBName,
				Type:       fieldType,
				IsPrimary:  field.PrimaryKey,
				IsNullable: !field.NotNull,
				Tag:        string(field.TagSettings["JSON"]),
			})
		}
	}

	// Add preloaded fields to the result
	if !exhaustive {
		if config, exists := tableConfigurations[tableName]; exists && len(config.Preloads) > 0 {
			for _, preload := range config.Preloads {
				// Get the JSON key from the mapping or use the preload name
				nameRaw := preload
				if mapping, ok := config.PreloadMappings[preload]; ok {
					nameRaw = mapping
				}

				// Add the preload field to the fields list
				fields = append(fields, FieldInfo{
					Name:       preload,
					NameRaw:    nameRaw,
					Type:       "object", // Preloaded fields are typically objects
					IsPrimary:  false,
					IsNullable: true,
					Tag:        "",
				})
			}
		}
	}

	tableInfo := TableInfo{
		Name:        tableName,
		Description: loadModelDescriptions(DB)[tableName],
		Fields:      fields,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tableInfo)
}
